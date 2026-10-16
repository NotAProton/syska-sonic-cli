package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/notaproton/syska-sonic-cli/internal/clierrors"
	"github.com/notaproton/syska-sonic-cli/internal/output"
)

// confirm runs the confirmation ladder: --yes/--force short-circuit; without
// them a TTY gets a prompt and all else fails with a teachable error
// (non-interactive by default, honest about why).
func (a *App) confirm(prompt string) error {
	if a.boolFlag("yes") || a.boolFlag("force") {
		return nil
	}
	if a.boolFlag("no-input") || !output.IsTTY(os.Stdin) {
		return clierrors.New(clierrors.CodeConfirm, "confirmation required for: %s", prompt).
			WithHint("stdin is not interactive; pass --yes (or --force) to proceed without a prompt")
	}
	fmt.Fprintf(os.Stderr, "%s [y/N]: ", prompt)
	var answer string
	if _, err := fmt.Scanln(&answer); err != nil {
		return clierrors.New(clierrors.CodeConfirm, "confirmation required for: %s", prompt).
			WithHint("unanswered prompt; pass --yes to proceed")
	}
	// tolerate stray flags pasted into the prompt: only explicit yes proceeds
	switch strings.TrimSpace(answer) {
	case "y", "Y", "yes", "YES", "Yes":
		return nil
	}
	return clierrors.New(clierrors.CodeConfirm, "declined: %s", prompt).
		WithHint("pass --yes to proceed non-interactively")
}
