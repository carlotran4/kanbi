package harness

import "time"

// Config describes the command-facing contract for one harness. These values
// are YAML-configurable, but the built-in defaults are kept next to the
// command/ref-capture behavior in this package so a supported harness contract
// can be inspected in one place.
type Config struct {
	Start []string `yaml:"start"`
	// StartWithPrompt, when non-empty, is used as the base command when
	// launching with PromptModeArg and a prompt is being sent. The prompt
	// text is appended as a final argument. If absent, Start is used.
	StartWithPrompt []string `yaml:"start_with_prompt"`
	Resume          []string `yaml:"resume"`
	Exit            []string `yaml:"exit"`
	PromptReady     string   `yaml:"prompt_ready"`
	PromptMode      string   `yaml:"prompt_mode"`
	SessionRef      string   `yaml:"session_ref"`
}

type CaptureFunc func(promptText string, since time.Time) (string, bool)

// Contract localizes the durable behavior for a supported harness: commands,
// prompt injection mode, exit keys, session-ref capture, and documentation map.
type Contract struct {
	Name       string
	Config     Config
	CaptureRef CaptureFunc
	DocsAnchor string
}

func BuiltinContracts() map[string]Contract {
	return map[string]Contract{
		"pi": {
			Name: "pi",
			Config: Config{
				Start:      []string{"pi"},
				Resume:     []string{"pi", "--session", "{session_ref}"},
				Exit:       []string{"C-c", "exit", "Enter"},
				PromptMode: PromptModeArg,
			},
			CaptureRef: latestPiSession,
			DocsAnchor: "docs/harness-contracts.md#pi",
		},
		"codex": {
			Name: "codex",
			Config: Config{
				Start:      []string{"codex", "--no-alt-screen"},
				Resume:     []string{"codex", "resume", "--no-alt-screen", "{session_ref}"},
				Exit:       []string{"C-c", "exit", "Enter"},
				PromptMode: PromptModeArg,
			},
			CaptureRef: latestCodexHistorySession,
			DocsAnchor: "docs/harness-contracts.md#codex",
		},
		"copilot": {
			Name: "copilot",
			Config: Config{
				Start:           []string{"copilot"},
				StartWithPrompt: []string{"copilot", "-i"},
				Resume:          []string{"copilot", "--resume={session_ref}"},
				Exit:            []string{"C-c", "exit", "Enter"},
				PromptMode:      PromptModeArg,
			},
			CaptureRef: latestCopilotSession,
			DocsAnchor: "docs/harness-contracts.md#copilot",
		},
		"claude": {
			Name: "claude",
			Config: Config{
				Start:      []string{"claude"},
				Resume:     []string{"claude", "--resume", "{session_ref}"},
				Exit:       []string{"C-c", "exit", "Enter"},
				PromptMode: PromptModeArg,
			},
			CaptureRef: latestClaudeSession,
			DocsAnchor: "docs/harness-contracts.md#claude",
		},
	}
}

func DefaultConfigs() map[string]Config {
	contracts := BuiltinContracts()
	out := make(map[string]Config, len(contracts))
	for name, contract := range contracts {
		out[name] = contract.Config
	}
	return out
}

func BuiltinContract(name string) (Contract, bool) {
	contract, ok := BuiltinContracts()[name]
	return contract, ok
}
