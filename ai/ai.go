package ai

import (
	"context"
	"fmt"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/openai"
	"github.com/scotthaleen/go-app"
)

type Config struct {
	APIKey       string
	Model        string
	SystemPrompt string
}

type Client struct {
	cfg   Config
	agent fantasy.Agent
}

func New(cfg Config) *Client {
	if cfg.Model == "" {
		cfg.Model = "gpt-4.1-mini"
	}
	return &Client{cfg: cfg}
}

func (c *Client) Component() *app.Component {
	return app.NewComponent(
		app.WithName("ai client"),
		app.WithOnStart(c.Start),
	)
}

func (c *Client) Start(ctx context.Context) error {
	if c.cfg.APIKey == "" {
		return fmt.Errorf("openai api key is required")
	}
	provider, err := openai.New(openai.WithAPIKey(c.cfg.APIKey), openai.WithUseResponsesAPI())
	if err != nil {
		return fmt.Errorf("create openai provider: %w", err)
	}
	model, err := provider.LanguageModel(ctx, c.cfg.Model)
	if err != nil {
		return fmt.Errorf("create language model: %w", err)
	}

	opts := []fantasy.AgentOption{}
	if c.cfg.SystemPrompt != "" {
		opts = append(opts, fantasy.WithSystemPrompt(c.cfg.SystemPrompt))
	}
	c.agent = fantasy.NewAgent(model, opts...)
	return nil
}

func (c *Client) Generate(ctx context.Context, prompt string) (string, error) {
	if c.agent == nil {
		return "", fmt.Errorf("ai client is not started")
	}
	result, err := c.agent.Generate(ctx, fantasy.AgentCall{Prompt: prompt})
	if err != nil {
		return "", err
	}
	return result.Response.Content.Text(), nil
}
