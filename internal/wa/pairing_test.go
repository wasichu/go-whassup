package wa

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow"
)

func TestCodePairRejectionFailsFast(t *testing.T) {
	w, got := newTestWhatsApp(t)
	// Errors from the same handler that aren't a rejection are ignored.
	w.clientError(codePairFailedFormat, []any{errors.New("pairing ref mismatch in code pair notification")})
	w.clientError("Failed to decrypt message: %s", []any{&whatsmeow.ElementMissingError{Tag: codePairRejectedTag}})
	if len(*got) != 0 {
		t.Fatalf("unrelated errors raised %+v", *got)
	}
	w.clientError(codePairFailedFormat, []any{&whatsmeow.ElementMissingError{Tag: codePairRejectedTag, In: "notification"}})
	if len(*got) != 1 || (*got)[0] != (PairFailed{Reason: codeRejected}) {
		t.Errorf("events = %+v, want the rejection", *got)
	}
}

func TestLoggerPassesErrorsToHook(t *testing.T) {
	var seen []string
	l := slogLogger{log: slog.New(slog.NewTextHandler(io.Discard, nil)), onError: func(f string, args []any) {
		seen = append(seen, fmt.Sprintf(f, args...))
	}}
	l.Sub("Socket").Errorf("boom %d", 1) // whatsmeow logs through sub-loggers
	l.Infof("not an error")
	if len(seen) != 1 || seen[0] != "boom 1" {
		t.Errorf("hook saw %q", seen)
	}
}

// TestCodePairRejectionStillLogged is a canary for whatsmeow upgrades: the
// fast pairing failure depends on how whatsmeow logs a rejected code.
func TestCodePairRejectionStillLogged(t *testing.T) {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "go.mau.fi/whatsmeow").Output()
	if err != nil {
		t.Skipf("can't locate whatsmeow's source: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(out)), "pair-code.go"))
	if err != nil {
		t.Skipf("can't read whatsmeow's pair-code.go: %v", err)
	}
	for _, want := range []string{
		fmt.Sprintf("cli.Log.Errorf(%q, err)", codePairFailedFormat),
		fmt.Sprintf("Tag: %q", codePairRejectedTag),
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("whatsmeow no longer has %s; update clientError (pairing.go)", want)
		}
	}
}
