package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	clnkr "github.com/clnkr-ai/clnkr"
)

// Compile-time assertion that model implements tea.Model.
var _ tea.Model = model{}

type focusTarget int

const (
	focusInput focusTarget = iota
	focusViewport
)

// ggTimeout is the maximum delay between the two 'g' presses for the gg chord.
const ggTimeout = 500 * time.Millisecond

// ctrlCInterval is the maximum gap between two Ctrl-C presses for a forced quit.
const ctrlCInterval = 500 * time.Millisecond

type agentDoneMsg struct{ err error }
type stepDoneMsg struct {
	result clnkr.StepResult
	err    error
}
type executeDoneMsg struct{ err error }

// tickMsg is sent by the elapsed-time ticker while the agent is running.
type tickMsg time.Time

// shared holds references that must survive bubbletea's value-copy of model.
// Bubbletea copies the model on NewProgram, so pointers stored directly in
// model fields are stale after that copy. This struct is allocated once and
// shared via pointer.
type shared struct {
	agent    *clnkr.Agent
	program  *tea.Program
	eventLog *os.File
	cwd      string
}

type model struct {
	chat                  chatModel
	input                 inputModel
	status                statusModel
	diff                  diffModel
	files                 fileTracker
	styles                *styles
	shared                *shared
	eventCh               chan clnkr.Event
	cancel                context.CancelFunc
	runCtx                context.Context
	width                 int
	height                int
	focus                 focusTarget
	running               bool
	quitting              bool
	agentErr              error
	pendingG              bool
	gTimer                time.Time
	verbose               bool
	lastCtrlC             time.Time
	fullSend              bool
	pendingAct            *clnkr.ActTurn
	awaitingApproval      bool
	awaitingClarification bool
	clarificationPrompt   string
	executedSteps         int
	protocolErrors        int
	closeEventChOnFinish  bool
	startupCmd            tea.Cmd
	exitOnRunFinish       bool
	bridgeDrained         bool
}

type modelOpts struct {
	eventCh         chan clnkr.Event
	styles          *styles
	verbose         bool
	cancel          context.CancelFunc
	modelName       string
	maxSteps        int
	fullSend        bool
	exitOnRunFinish bool
}

func newModel(opts modelOpts) model {
	return model{
		chat:            newChatModel(0, 0, opts.styles, opts.verbose),
		input:           newInputModel(0, &opts.styles.Input),
		status:          newStatusModel(opts.modelName, opts.maxSteps, &opts.styles.Status),
		diff:            newDiffModel(opts.styles),
		styles:          opts.styles,
		shared:          &shared{},
		eventCh:         opts.eventCh,
		cancel:          opts.cancel,
		focus:           focusInput,
		verbose:         opts.verbose,
		fullSend:        opts.fullSend,
		exitOnRunFinish: opts.exitOnRunFinish,
	}
}

// inputHeight returns the height the input area should occupy.
func (m model) inputHeight() int {
	h := m.input.lineCount() + 1
	if h > 5 {
		h = 5
	}
	return h
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.input.textarea.Focus()}
	if m.eventCh != nil {
		cmds = append(cmds, eventBridge(m.eventCh))
	}
	if m.startupCmd != nil {
		cmds = append(cmds, m.startupCmd)
	}
	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case nil:
		if m.exitOnRunFinish {
			m.bridgeDrained = true
			if !m.running {
				return m, tea.Quit
			}
		}
		return m, nil

	case tea.QuitMsg:
		m.quitting = true
		return m, tea.Quit

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.recalcLayout()
		chatH := m.height - statusHeight - m.inputHeight()
		if chatH < 1 {
			chatH = 1
		}
		m.diff.resize(m.width, chatH)
		return m, nil

	case agentDoneMsg:
		m.finishRun(msg.err)
		if m.exitOnRunFinish && m.bridgeDrained {
			return m, tea.Quit
		}
		return m, nil

	case stepDoneMsg:
		return m.handleStepDone(msg)

	case executeDoneMsg:
		return m.handleExecuteDone(msg)

	case eventMsg:
		m.routeEvent(msg.event)
		m.chat.updateViewport()
		if m.eventCh != nil {
			return m, eventBridge(m.eventCh)
		}
		return m, nil

	case tickMsg:
		if m.running {
			return m, tickCmd()
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	default:
		// Forward unrecognized messages (e.g. cursor blink) to the textarea
		// so its internal commands keep working.
		newTA, cmd := m.input.textarea.Update(msg)
		m.input.textarea = newTA
		return m, cmd
	}
}

const statusHeight = 1

// recalcLayout uses a pointer receiver because it calls pointer-receiver
// methods on chatModel. Called from value-receiver methods on model —
// Go takes &m on the local copy, mutations are preserved through return.
func (m *model) recalcLayout() {
	ih := m.inputHeight()
	chatHeight := m.height - statusHeight - ih
	if chatHeight < 1 {
		chatHeight = 1
	}
	m.chat.resize(m.width, chatHeight)
	m.chat.updateViewport()
	m.input.setWidth(m.width)
}

func (m *model) finishRun(err error) {
	m.running = false
	m.agentErr = err
	m.status.stopRun()
	m.pendingAct = nil
	m.awaitingApproval = false
	m.awaitingClarification = false
	m.clarificationPrompt = ""
	m.executedSteps = 0
	m.protocolErrors = 0
	if m.chat.streaming {
		m.chat.commitPartialStream()
	}
	if err != nil && !errors.Is(err, clnkr.ErrClarificationNeeded) && !errors.Is(err, context.Canceled) {
		m.chat.content.WriteString(
			m.styles.Chat.Warning.Render(fmt.Sprintf("\n%s Agent error: %s", iconError, err)),
		)
		m.chat.content.WriteString("\n\n")
	}
	m.chat.updateViewport()
	if m.closeEventChOnFinish && m.eventCh != nil {
		close(m.eventCh)
		m.eventCh = nil
	}
	m.closeEventChOnFinish = false
}

func stepCmd(agent *clnkr.Agent, ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		result, err := agent.Step(ctx)
		return stepDoneMsg{result: result, err: err}
	}
}

func executeCmd(agent *clnkr.Agent, ctx context.Context, act *clnkr.ActTurn) tea.Cmd {
	return func() tea.Msg {
		_, err := agent.ExecuteTurn(ctx, act)
		return executeDoneMsg{err: err}
	}
}

func requestFinalSummaryCmd(agent *clnkr.Agent, ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		return agentDoneMsg{err: agent.RequestStepLimitSummary(ctx)}
	}
}

func (m model) handleStepDone(msg stepDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.finishRun(msg.err)
		if m.exitOnRunFinish && m.bridgeDrained {
			return m, tea.Quit
		}
		return m, nil
	}

	if msg.result.ParseErr != nil {
		m.protocolErrors++
		if m.protocolErrors >= 3 {
			m.finishRun(fmt.Errorf("consecutive protocol failures, exiting"))
			return m, nil
		}
		return m, stepCmd(m.shared.agent, m.runCtx)
	}
	m.protocolErrors = 0

	switch turn := msg.result.Turn.(type) {
	case *clnkr.DoneTurn:
		m.finishRun(nil)
		return m, nil
	case *clnkr.ClarifyTurn:
		m.awaitingClarification = true
		m.clarificationPrompt = turn.Question
		m.chat.appendHostNote(turn.Question)
		m.chat.updateViewport()
		m.focus = focusInput
		m.status.setFocus(focusInput)
		return m, m.input.textarea.Focus()
	case *clnkr.ActTurn:
		m.pendingAct = turn
		m.awaitingApproval = true
		m.chat.setProposedCommand(turn.Command)
		m.chat.updateViewport()
		return m, nil
	default:
		m.finishRun(fmt.Errorf("unexpected turn type %T", turn))
		return m, nil
	}
}

func (m model) handleExecuteDone(msg executeDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.finishRun(msg.err)
		if m.exitOnRunFinish && m.bridgeDrained {
			return m, tea.Quit
		}
		return m, nil
	}

	m.pendingAct = nil
	m.awaitingApproval = false
	m.executedSteps++
	if m.shared.agent.MaxSteps > 0 && m.executedSteps >= m.shared.agent.MaxSteps {
		return m, requestFinalSummaryCmd(m.shared.agent, m.runCtx)
	}
	return m, stepCmd(m.shared.agent, m.runCtx)
}

// routeEvent dispatches a core library event to the appropriate sub-models.
func (m *model) routeEvent(e clnkr.Event) {
	// Capture lastCmd before appendEvent clears it on EventCommandDone
	lastCmd := m.chat.lastCmd

	m.chat.appendEvent(e)
	switch ev := e.(type) {
	case clnkr.EventResponse:
		m.status.updateFromResponse(ev.Usage)
	case clnkr.EventCommandDone:
		m.status.incrementStep()
		m.files.trackFromCommand(lastCmd, ev.Stdout)
	case clnkr.EventCommandStart, clnkr.EventProtocolFailure, clnkr.EventDebug:
		// handled by chat.appendEvent only
	default:
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Diff overlay active — intercept all keys
	if m.diff.visible {
		return m.handleDiffKey(msg)
	}

	// gg chord handling
	if m.pendingG {
		m.pendingG = false
		if msg.Code == 'g' && time.Since(m.gTimer) < ggTimeout {
			m.chat.viewport.GotoTop()
			m.chat.hasNew = false
			m.chat.updateViewport()
			return m, nil
		}
		// Timeout or different key — fall through to normal handling
	}

	switch {
	case msg.Code == 'c' && msg.Mod == tea.ModCtrl:
		return m.handleCtrlC()

	case m.awaitingApproval && msg.Code == 'y':
		return m.approvePendingCommand()

	case m.awaitingApproval && msg.Code == 'n':
		return m.rejectPendingCommand()

	case msg.Code == tea.KeyEscape:
		m.focus = focusViewport
		m.status.setFocus(focusViewport)
		m.input.textarea.Blur()
		return m, nil

	case msg.Code == 'i' && m.focus == focusViewport:
		m.focus = focusInput
		m.status.setFocus(focusInput)
		return m, m.input.textarea.Focus()

	case msg.Code == 'q' && m.focus == focusViewport:
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit
	}

	// Focus-specific key handling
	if m.focus == focusViewport {
		return m.handleViewportKey(msg)
	}

	return m.handleInputKey(msg)
}

func (m model) handleCtrlC() (tea.Model, tea.Cmd) {
	now := time.Now()
	doublePress := now.Sub(m.lastCtrlC) < ctrlCInterval
	m.lastCtrlC = now

	if doublePress || !m.running {
		// Double rapid Ctrl-C or idle: always quit
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit
	}

	// Running: cancel the agent but don't quit
	if m.awaitingApproval || m.awaitingClarification {
		m.finishRun(context.Canceled)
		return m, nil
	}
	if m.cancel != nil {
		m.cancel()
	}
	return m, nil
}

func (m model) handleInputKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Code == tea.KeyEnter && msg.Mod == 0:
		// Submit on Enter (no modifiers)
		if m.awaitingClarification {
			text := m.input.submit()
			if text == "" {
				return m, nil
			}
			m.awaitingClarification = false
			m.clarificationPrompt = ""
			m.chat.appendUserMessage(text)
			m.shared.agent.AppendUserMessage(text)
			m.chat.updateViewport()
			return m, stepCmd(m.shared.agent, m.runCtx)
		}
		if m.running {
			return m, nil // reject while task is active
		}
		text := m.input.submit()
		if text == "" {
			return m, nil
		}
		return m.startTask(text)

	case msg.Code == 'j' && msg.Mod == tea.ModCtrl:
		// Ctrl+J inserts a newline
		m.input.textarea.InsertRune('\n')
		m.recalcLayout()
		return m, nil

	case msg.Code == tea.KeyUp && m.input.textarea.Line() == 0:
		// Up at first line: history
		m.input.historyUp()
		return m, nil

	case msg.Code == tea.KeyDown && m.input.textarea.Line() == m.input.textarea.LineCount()-1:
		// Down at last line: history
		m.input.historyDown()
		return m, nil

	case msg.Mod == tea.ModCtrl && msg.Code == 'u':
		// Ctrl+U scrolls viewport even in input mode
		m.chat.viewport.HalfPageUp()
		if m.chat.viewport.AtBottom() {
			m.chat.hasNew = false
		}
		return m, nil

	case msg.Mod == tea.ModCtrl && msg.Code == 'd':
		// Ctrl+D: scroll viewport when empty or running, otherwise pass to textarea
		if m.input.textarea.Value() == "" || m.running {
			m.chat.viewport.HalfPageDown()
			if m.chat.viewport.AtBottom() {
				m.chat.hasNew = false
			}
			return m, nil
		}
	}

	// Pass through to textarea
	newTA, cmd := m.input.textarea.Update(msg)
	m.input.textarea = newTA
	m.recalcLayout()
	return m, cmd
}

func (m model) startTask(task string) (tea.Model, tea.Cmd) {
	// Show user's task in chat
	m.chat.appendUserMessage(task)
	m.chat.updateViewport()

	m.running = true
	m.status.startRun()
	m.executedSteps = 0
	m.protocolErrors = 0
	m.pendingAct = nil
	m.awaitingApproval = false
	m.awaitingClarification = false
	m.clarificationPrompt = ""

	// Create new context for this task
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.runCtx = ctx

	// Set up event channel for this run
	eventCh := make(chan clnkr.Event, eventChSize)
	m.eventCh = eventCh
	m.shared.agent.Notify = makeNotify(eventCh, m.shared.eventLog)

	agent := m.shared.agent

	if m.fullSend {
		cmd := func() tea.Msg {
			runErr := agent.Run(ctx, task)
			close(eventCh)
			return agentDoneMsg{err: runErr}
		}
		return m, tea.Batch(cmd, eventBridge(eventCh), tickCmd())
	}

	m.closeEventChOnFinish = true
	m.shared.agent.AppendUserMessage(task)
	return m, tea.Batch(stepCmd(agent, ctx), eventBridge(eventCh), tickCmd())
}

func (m model) approvePendingCommand() (tea.Model, tea.Cmd) {
	if m.pendingAct == nil {
		return m, nil
	}
	m.awaitingApproval = false
	return m, executeCmd(m.shared.agent, m.runCtx, m.pendingAct)
}

func (m model) rejectPendingCommand() (tea.Model, tea.Cmd) {
	m.awaitingApproval = false
	m.awaitingClarification = true
	m.clarificationPrompt = "Command denied. What should the agent do instead?"
	m.pendingAct = nil
	m.chat.clearPendingCommand()
	m.chat.appendHostNote(m.clarificationPrompt)
	m.chat.updateViewport()
	m.focus = focusInput
	m.status.setFocus(focusInput)
	return m, m.input.textarea.Focus()
}

func (m model) handleDiffKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Code == tea.KeyEscape, msg.Code == 'd':
		m.diff.visible = false
	case msg.Code == 'q':
		m.diff.visible = false
	case msg.Code == 'j':
		m.diff.viewport.ScrollDown(1)
	case msg.Code == 'k':
		m.diff.viewport.ScrollUp(1)
	case msg.Mod == tea.ModCtrl && msg.Code == 'd':
		m.diff.viewport.HalfPageDown()
	case msg.Mod == tea.ModCtrl && msg.Code == 'u':
		m.diff.viewport.HalfPageUp()
	case msg.Code == 'G':
		m.diff.viewport.GotoBottom()
	case msg.Code == 'g':
		// Simple single-g for top in diff mode (no chord needed)
		m.diff.viewport.GotoTop()
	}
	return m, nil
}

func (m model) handleViewportKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Code == 'j':
		m.chat.viewport.ScrollDown(1)
	case msg.Code == 'k':
		m.chat.viewport.ScrollUp(1)
	case msg.Code == 'G':
		m.chat.viewport.GotoBottom()
		m.chat.hasNew = false
	case msg.Code == 'g':
		m.pendingG = true
		m.gTimer = time.Now()
		return m, nil
	case msg.Mod == tea.ModCtrl && msg.Code == 'd':
		m.chat.viewport.HalfPageDown()
	case msg.Mod == tea.ModCtrl && msg.Code == 'u':
		m.chat.viewport.HalfPageUp()
	case msg.Code == tea.KeyPgUp:
		m.chat.viewport.HalfPageUp()
	case msg.Code == tea.KeyPgDown:
		m.chat.viewport.HalfPageDown()
	case msg.Code == tea.KeyHome:
		m.chat.viewport.GotoTop()
		m.chat.hasNew = false
	case msg.Code == tea.KeyEnd:
		m.chat.viewport.GotoBottom()
		m.chat.hasNew = false
	case msg.Code == 'f' && msg.Mod == tea.ModCtrl:
		// Ctrl+F toggles diff overlay
		chatH := m.height - statusHeight - m.inputHeight()
		if chatH < 1 {
			chatH = 1
		}
		m.diff.toggle(m.files.files, m.shared.cwd, m.width, chatH)
		return m, nil
	}

	if m.chat.viewport.AtBottom() {
		m.chat.hasNew = false
	}

	return m, nil
}

func (m model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}

	var topPane string
	if m.diff.visible {
		topPane = m.diff.view()
	} else {
		topPane = m.chat.viewport.View()
		// New content indicator
		if m.chat.hasNew {
			indicator := m.styles.Chat.NewContent.Render(
				fmt.Sprintf(" %s new content ", iconNewContent),
			)
			topPane += "\n" + indicator
		}
	}

	statusView := m.status.view(m.width)
	inputView := m.input.view()

	return tea.NewView(topPane + "\n" + statusView + "\n" + inputView)
}
