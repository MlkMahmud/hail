package commands

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/codecrafters-io/bittorrent-starter-go/app/bencode"
	"github.com/urfave/cli/v2"
)

func HandleDecode(ctx *cli.Context) error {
	bencodedStr := ctx.Args().Get(0)
	decoded, _, err := bencode.DecodeValue([]byte(bencodedStr))

	if err != nil {
		return err
	}

	jsonString, err := json.Marshal(decoded)

	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(string(jsonString))
	return nil
}
