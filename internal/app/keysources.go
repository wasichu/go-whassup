package app

import (
	"context"
	"fmt"
	"os"

	"github.com/wasichu/go-whassup/internal/config"
	"github.com/wasichu/go-whassup/internal/encryption"
	"github.com/wasichu/go-whassup/internal/keysource"
	"github.com/wasichu/go-whassup/internal/paths"
)

func realKeySources(cfg config.Config, p paths.Paths, stdio IO) encryption.Sources {
	return encryption.Sources{
		Keyring: keysource.NewKeyring(),
		Passphrase: func(ctx context.Context, src string, confirm bool) ([]byte, error) {
			if src == config.KeySourcePasswordCommand {
				return keysource.Command{Cmd: cfg.Encryption.PasswordCommand, ConfigPath: p.ConfigFile}.Passphrase(ctx)
			}
			in, _ := stdio.In.(*os.File)
			label := "Passphrase for stored messages: "
			if confirm {
				if _, err := fmt.Fprintln(stdio.Err, "Setting up encryption of stored messages (message text only;\nwho you talk to and when stays readable)."); err != nil {
					return nil, err
				}
				label = "New passphrase: "
			}
			return keysource.Prompt{In: in, Out: stdio.Err}.Passphrase(label, confirm)
		},
	}
}
