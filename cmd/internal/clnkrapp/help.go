package clnkrapp

const ProviderOptionsUsage = `Provider options:
      --model string        Model identifier (required; env: $CLNKR_MODEL)
      --base-url string     LLM endpoint transport URL (env: $CLNKR_BASE_URL)
      --provider string     Provider adapter: anthropic|openai
      --provider-api string OpenAI API override
      --act-protocol string Act protocol: clnkr-inline|tool-calls
      --effort string       Provider effort: auto|low|medium|high|xhigh|max
      --max-output-tokens int Maximum response output tokens
      --thinking-budget-tokens int
                            Anthropic legacy/debug thinking budget override
`

const SystemPromptUsage = `System prompt:
      --no-system-prompt              Skip the built-in system prompt entirely
      --system-prompt-append string   Append text to the built-in system prompt
      --dump-system-prompt            Print the composed system prompt and exit
`

const EnvironmentUsage = `Environment:
  CLNKR_API_KEY      API key for the LLM provider (required)
  CLNKR_PROVIDER     Provider adapter semantics
  CLNKR_PROVIDER_API OpenAI-only API surface override
  CLNKR_MODEL        Model identifier override
  CLNKR_BASE_URL     Endpoint override; infers provider when provider is unset
`
