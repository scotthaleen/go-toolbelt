// Package ai provides a small go-app component for text generation through
// Charm Fantasy and an OpenAI-compatible model.
//
// A Client validates and constructs its provider during app startup. Generate
// is available after the component has started. The default model is the
// direct OpenAI model gpt-4.1-mini. This package is intentionally a narrow
// recipe rather than a general abstraction over AI providers.
package ai
