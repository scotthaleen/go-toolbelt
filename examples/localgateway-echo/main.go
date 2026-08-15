package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/scotthaleen/go-app"
	"github.com/scotthaleen/go-toolbelt/localgateway"
)

type message struct {
	Message string `json:"message"`
}

func main() {
	if len(os.Args) < 2 {
		fatal("usage: localgateway-echo serve | echo <message>")
	}
	endpoint, err := echoEndpoint()
	if err != nil {
		fatal(err.Error())
	}
	switch os.Args[1] {
	case "serve":
		if err := serve(endpoint); err != nil {
			fatal(err.Error())
		}
	case "echo":
		if len(os.Args) != 3 {
			fatal("usage: localgateway-echo echo <message>")
		}
		if err := call(endpoint, os.Args[2]); err != nil {
			fatal(err.Error())
		}
	default:
		fatal("usage: localgateway-echo serve | echo <message>")
	}
}

func serve(endpoint string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/echo", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(io.LimitReader(r.Body, 4097))
		if err != nil || len(data) > 4096 {
			http.Error(w, "invalid message", http.StatusBadRequest)
			return
		}
		var input message
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || input.Message == "" || decoder.Decode(&struct{}{}) != io.EOF {
			http.Error(w, "invalid message", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(input)
	})
	gateway := localgateway.New(localgateway.DefaultConfig(endpoint), mux)
	application := app.New(context.Background(), app.WithSequentialStartup(app.Managed(gateway)))
	fmt.Printf("starting echo gateway at %s\n", endpoint)
	return application.Run()
}

func call(endpoint, value string) error {
	body, err := json.Marshal(message{Message: value})
	if err != nil {
		return err
	}
	client := localgateway.NewClient(endpoint, localgateway.ClientConfig{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, localgateway.BaseURL+"/v1/echo", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("gateway returned %s", response.Status)
	}
	var output message
	if err := json.NewDecoder(io.LimitReader(response.Body, 4097)).Decode(&output); err != nil {
		return err
	}
	fmt.Println(output.Message)
	return nil
}

func fatal(message string) {
	if message == "" {
		message = errors.New("local gateway failed").Error()
	}
	_, _ = fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
