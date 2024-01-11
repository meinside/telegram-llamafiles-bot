package main

import (
	"fmt"
	"log"
	"os"
)

func main() {
	if len(os.Args) > 1 {
		if conf, err := readConfig(os.Args[1]); err == nil {
			runBot(conf)
		} else {
			log.Printf("failed to read config file: %s", err)
		}
	} else {
		showHelp()
	}
}

func showHelp() {
	fmt.Printf(`Usage:

  $ %[1]s [CONFIG_FILEPATH]

Example:

  $ %[1]s ./config.json
`, os.Args[0])
}
