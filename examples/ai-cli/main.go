package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/scotthaleen/go-app"
	"github.com/scotthaleen/go-toolbelt/ai"
	"github.com/scotthaleen/go-toolbelt/logging"
)

func main() {
	verbosity := countVerbosity(os.Args[1:])
	logger := logging.Setup(logging.Config{Verbosity: verbosity, AddSource: true})

	prompt := strings.TrimSpace(strings.Join(nonFlagArgs(os.Args[1:]), " "))
	if prompt == "" {
		prompt = "Say hello from a go-app component in one sentence."
	}

	client := ai.New(ai.Config{
		APIKey:       os.Getenv("OPENAI_API_KEY"),
		Model:        getenv("OPENAI_MODEL", "gpt-4.1-mini"),
		SystemPrompt: "You are a concise assistant used in a Go lifecycle example.",
	})

	a := app.New(
		context.Background(),
		app.WithSignalHandling(false),
		app.WithLogger(logger),
		app.WithSequentialStartup(app.Registered(client)),
	)

	if err := a.Start(context.Background()); err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := a.Close(context.Background()); err != nil {
			log.Printf("shutdown failed: %v", err)
		}
	}()

	callCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	text, err := client.Generate(callCtx, prompt)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(text)
}

func getenv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func countVerbosity(args []string) int {
	verbosity := 0
	for _, arg := range args {
		if len(arg) > 1 && arg[0] == '-' {
			for _, r := range arg[1:] {
				if r == 'v' {
					verbosity++
				}
			}
		}
	}
	return verbosity
}

func nonFlagArgs(args []string) []string {
	values := []string{}
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			values = append(values, arg)
		}
	}
	return values
}
