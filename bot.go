package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tg "github.com/meinside/telegram-bot-go"
)

const (
	PollingIntervalSeconds = 1

	RequestQueueSize = 10
	ProcessQueueSize = 1
)

// struct for config.json
type config struct {
	TelegramBotToken         string   `json:"telegram_bot_token"`
	AllowedTelegramUsernames []string `json:"allowed_telegram_usernames,omitempty"`

	Models []model `json:"models"`
}

// model struct in config
type model struct {
	LlamafilePath              *string  `json:"llamafile_path,omitempty"`
	LlamafilePromptPattern     *string  `json:"llamafile_prompt_pattern,omitempty"`
	LlamafilePromptPlaceholder *string  `json:"llamafile_prompt_placeholder,omitempty"`
	LlamafileOtherParameters   []string `json:"llamafile_other_parameters,omitempty"`

	Disabled bool `json:"disabled,omitempty"`
}

// for debug-printing models
func (m model) String() string {
	var str string

	if m.LlamafilePath != nil && m.LlamafilePromptPattern != nil && m.LlamafilePromptPlaceholder != nil { // or Llamafile,
		str = fmt.Sprintf("Llamafile (%s)", filepath.Base(*m.LlamafilePath))
	} else {
		str = "misconfigured model"
	}

	return str
}

// request struct
type request struct {
	model model

	originalText *string
	commentText  *string

	targetChatID    int64
	targetMessageID int64

	startedProcessingAt time.Time
}

// read, parse, and return the parsed config from the given filepath (json format)
func readConfig(path string) (conf config, err error) {
	var bytes []byte
	if bytes, err = os.ReadFile(path); err == nil {
		if err = json.Unmarshal(bytes, &conf); err == nil {
			return conf, nil
		}
	}

	return config{}, err
}

// check if given update is allowed to handle
//
// NOTE: if `allowed_telegram_usernames` is empty, every update will be allowed
func allowed(conf config, update tg.Update) bool {
	if update.Message.From != nil && update.Message.From.Username != nil {
		for _, username := range conf.AllowedTelegramUsernames {
			if *update.Message.From.Username != username {
				return false
			}
		}
		return true
	}

	return false
}

// escapes given text for using in shell (llamafile execution)
func escapeForShell(text string) string {
	return strings.ReplaceAll(text, "\"", "”")
}

// escapes given text for using in HTML parse mode ('<', '>', and '&')
func escapeForHTML(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(text, "&", "&amp;"), ">", "&gt;"), "<", "&lt;")
}

func runBot(conf config) {
	bot := tg.NewClient(conf.TelegramBotToken)

	if me := bot.GetMe(); me.Ok {
		requestQueue := make(chan request, RequestQueueSize)
		processQueue := make(chan request, ProcessQueueSize)

		// handle enqueued request queue
		go func() {
			for request := range requestQueue {
				processQueue <- request
			}
		}()

		// process requests
		go func() {
			for request := range processQueue {
				handleRequest(conf, bot, request)
			}
		}()

		// poll updates and handle them
		bot.StartPollingUpdates(0, PollingIntervalSeconds, func(c *tg.Bot, update tg.Update, err error) {
			// skip it if it has no message or text content
			if !update.HasMessage() || !update.Message.HasText() {
				return
			}

			// skip it if it is from a non-allowed user
			if !allowed(conf, update) {
				return
			}

			// skip if it is an ignorable message or command
			if *update.Message.Text == "/start" {
				return
			}

			// handle comment request
			if update.Message.HasReplyTo() && update.Message.ReplyToMessage.HasText() { // it has a parent message (is a comment)
				// get texts from the message, and cleanse them
				originalText := escapeForShell(*update.Message.ReplyToMessage.Text)
				commentText := escapeForShell(*update.Message.Text)

				// and enquene requests
				for _, model := range conf.Models {
					// skip disabled models
					if model.Disabled {
						continue
					}

					enqueueRequest(requestQueue, model, &originalText, &commentText, update.Message.Chat.ID, update.Message.MessageID)
				}
			} else { // handle message request
				// get texts from the message, and cleanse them
				originalText := escapeForShell(*update.Message.Text)

				// and enquene requests
				for _, model := range conf.Models {
					// skip disabled models
					if model.Disabled {
						continue
					}

					enqueueRequest(requestQueue, model, &originalText, nil, update.Message.Chat.ID, update.Message.MessageID)
				}
			}
		})
	} else {
		log.Printf("failed to get info about this bot: %s", *me.Description)
	}
}

// enqueue request
func enqueueRequest(reqQueue chan request, model model, originalText, commentText *string, chatID, messageID int64) {
	go func(queue chan request) {
		if originalText != nil && commentText != nil {
			log.Printf(`>>> enqueueing request with
- model: %s
- originalText: %s
- commentText: %s`, model, *originalText, *commentText)

			request := request{
				model: model,

				originalText: originalText,
				commentText:  commentText,

				targetChatID:    chatID,
				targetMessageID: messageID,
			}

			queue <- request
		} else if originalText != nil {
			log.Printf(`>>> enqueueing request with
- model: %s
- originalText: %s`, model, *originalText)

			request := request{
				model: model,

				originalText: originalText,
				commentText:  nil,

				targetChatID:    chatID,
				targetMessageID: messageID,
			}

			queue <- request
		}
	}(reqQueue)
}

// handle request which was dequeued from the request queue
func handleRequest(conf config, bot *tg.Bot, request request) {
	request.startedProcessingAt = time.Now()

	log.Printf(">>> handling request: %+v", request)

	var generated string

	model := request.model
	if model.LlamafilePath != nil && model.LlamafilePromptPattern != nil && model.LlamafilePromptPlaceholder != nil { // or Llamafile,
		generated = handleLlamafileRequest(conf, request)
	} else {
		generated = fmt.Sprintf("Error: misconfiguration in your config (%s)", model)
	}

	// send the result to telegram
	options := tg.OptionsSendMessage{}.
		SetReplyParameters(tg.ReplyParameters{MessageID: request.targetMessageID}).
		SetParseMode(tg.ParseModeHTML)
	if sent := bot.SendMessage(request.targetChatID, generated, options); !sent.Ok {
		log.Printf("Error: failed to send message to telegram: %s", *sent.Description)
	}
}

func handleLlamafileRequest(conf config, request request) string {
	model := request.model

	var prompt string
	if request.originalText != nil && request.commentText != nil {
		prompt = strings.ReplaceAll(*model.LlamafilePromptPattern, *model.LlamafilePromptPlaceholder, fmt.Sprintf("%s: %s", *request.commentText, *request.originalText))
	} else if request.originalText != nil {
		prompt = strings.ReplaceAll(*model.LlamafilePromptPattern, *model.LlamafilePromptPlaceholder, *request.originalText)
	} else if request.commentText != nil {
		prompt = strings.ReplaceAll(*model.LlamafilePromptPattern, *model.LlamafilePromptPlaceholder, *request.commentText)
	}

	if generated, err := generateFromLlamafile(*model.LlamafilePath, prompt, model.LlamafileOtherParameters...); err == nil {
		return `<pre><code>
` + escapeForHTML(generated) + `
</code></pre>

` + additionalGenerationInfo(request, filepath.Base(*model.LlamafilePath))
	} else {
		return fmt.Sprintf(`Failed to generate from prompt '%s' and parameters: %+v: <em>%s</em>`, prompt, model.LlamafileOtherParameters, escapeForHTML(err.Error()))
	}
}

// generate text with `llamafile`
//
// NOTE: tested only on macOS
// FIXME: without `bash`, llamafile fails to run
func generateFromLlamafile(llamafilePath, prompt string, params ...string) (string, error) {
	ps := []string{llamafilePath, "-p", fmt.Sprintf("\"%s\"", prompt)}
	ps = append(ps, params...)
	ps = append(ps, "--silent-prompt")

	//log.Printf(">>> running: $ bash %s", strings.Join(ps, " "))

	cmd := exec.Command("bash", ps...)
	if out, err := cmd.Output(); err == nil {
		return strings.TrimSpace(string(out)), nil
	} else {
		return "", fmt.Errorf("Failed to run '%s' with params %+v: %s", llamafilePath, params, err)
	}
}

// generate an additional info about the generation
func additionalGenerationInfo(request request, model string) string {
	elapsedSinceProcessing := time.Since(request.startedProcessingAt).Milliseconds()

	return fmt.Sprintf(`<em>(request was processed by <strong>%s</strong> in %s seconds)</em>`,
		model,
		msecsToString(elapsedSinceProcessing),
	)
}

// for converting msecs into "0.000" format
func msecsToString(msecs int64) string {
	return fmt.Sprintf("%d.%03d", msecs/1000, msecs%1000)
}
