# telegram-llamafiles-bot

A telegram bot for testing various [llamafiles](https://github.com/Mozilla-Ocho/llamafile) on a local machine.

## Usage

Build with:

```bash
$ git clone https://github.com/meinside/telegram-llamafiles-bot.git
$ cd telegram-llamafiles-bot/
$ go build
```

create a `config.json` file of your own, and run with:

```bash
$ cp ./config.json.sample ./config.json
$ vi ./config.json
$ ./telegram-llamafiles-bot ./config.json
```

You can see the sample configurations in the `config.json.sample` file.

## Note

Tested only on macOS Sonoma.

## License

MIT

