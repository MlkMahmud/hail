package commands

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/MlkMahmud/hail/session"
	"github.com/MlkMahmud/hail/utils"
	"github.com/urfave/cli/v2"
)

func HandleDownloadCommand(ctx *cli.Context) error {
	src := ctx.Args().First()

	sessionId := [20]byte{}
	copy(sessionId[:], []byte(fmt.Sprintf("-HA001-%s", utils.GenerateRandomString(15, ""))))

	sesh := session.NewSession(sessionId)

	if err := sesh.AddTorrent(src); err != nil {
		return err
	}

	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, syscall.SIGINT, syscall.SIGTERM)

	<-sigC
	sesh.Stop()

	return nil
}
