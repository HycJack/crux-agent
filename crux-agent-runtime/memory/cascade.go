package memory

import (
	"context"
	"fmt"
	"log/slog"
)

// CascadeProvider queries multiple providers in sequence,
// returning the first successful result.
type CascadeProvider struct {
	providers []MemoryProvider
	logger    *slog.Logger
}

// NewCascadeProvider creates a new cascade provider.
func NewCascadeProvider(providers ...MemoryProvider) *CascadeProvider {
	return &CascadeProvider{
		providers: providers,
		logger:    slog.Default(),
	}
}

func (p *CascadeProvider) Store(ctx context.Context, entry Entry) error {
	// Store to all providers
	var errs []error
	for _, provider := range p.providers {
		if err := provider.Store(ctx, entry); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == len(p.providers) {
		return fmt.Errorf("all providers failed to store: %v", errs)
	}
	return nil
}

func (p *CascadeProvider) Search(ctx context.Context, query string, limit int) ([]Entry, error) {
	var lastErr error
	for _, provider := range p.providers {
		entries, err := provider.Search(ctx, query, limit)
		if err == nil {
			return entries, nil
		}
		lastErr = err
		p.logger.Warn("provider search failed, trying next", "error", err)
	}
	return nil, fmt.Errorf("all providers failed: %w", lastErr)
}

func (p *CascadeProvider) Get(ctx context.Context, key string) (Entry, bool, error) {
	var lastErr error
	for _, provider := range p.providers {
		entry, found, err := provider.Get(ctx, key)
		if err == nil && found {
			return entry, true, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr != nil {
		return Entry{}, false, lastErr
	}
	return Entry{}, false, nil
}

func (p *CascadeProvider) Delete(ctx context.Context, key string) error {
	var errs []error
	for _, provider := range p.providers {
		if err := provider.Delete(ctx, key); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == len(p.providers) {
		return fmt.Errorf("all providers failed to delete: %v", errs)
	}
	return nil
}

func (p *CascadeProvider) List(ctx context.Context, filter Filter) ([]Entry, error) {
	// Use first provider
	return p.providers[0].List(ctx, filter)
}

func (p *CascadeProvider) FormatForPrompt(ctx context.Context, query string, limit int) (string, error) {
	entries, err := p.Search(ctx, query, limit)
	if err != nil {
		return "", err
	}
	return formatEntries(entries), nil
}

func (p *CascadeProvider) Close() error {
	for _, provider := range p.providers {
		if err := provider.Close(); err != nil {
			return err
		}
	}
	return nil
}

// PrimaryFallbackProvider uses primary provider, falls back to secondary on failure.
type PrimaryFallbackProvider struct {
	primary  MemoryProvider
	fallback MemoryProvider
	logger   *slog.Logger
}

// NewPrimaryFallbackProvider creates a new primary/fallback provider.
func NewPrimaryFallbackProvider(primary, fallback MemoryProvider) *PrimaryFallbackProvider {
	return &PrimaryFallbackProvider{
		primary:  primary,
		fallback: fallback,
		logger:   slog.Default(),
	}
}

func (p *PrimaryFallbackProvider) Store(ctx context.Context, entry Entry) error {
	err := p.primary.Store(ctx, entry)
	if err != nil {
		p.logger.Warn("primary store failed, using fallback", "error", err)
		return p.fallback.Store(ctx, entry)
	}
	return nil
}

func (p *PrimaryFallbackProvider) Search(ctx context.Context, query string, limit int) ([]Entry, error) {
	entries, err := p.primary.Search(ctx, query, limit)
	if err != nil {
		p.logger.Warn("primary search failed, using fallback", "error", err)
		return p.fallback.Search(ctx, query, limit)
	}
	return entries, nil
}

func (p *PrimaryFallbackProvider) Get(ctx context.Context, key string) (Entry, bool, error) {
	entry, found, err := p.primary.Get(ctx, key)
	if err != nil {
		p.logger.Warn("primary get failed, using fallback", "error", err)
		return p.fallback.Get(ctx, key)
	}
	return entry, found, nil
}

func (p *PrimaryFallbackProvider) Delete(ctx context.Context, key string) error {
	err := p.primary.Delete(ctx, key)
	if err != nil {
		return p.fallback.Delete(ctx, key)
	}
	return nil
}

func (p *PrimaryFallbackProvider) List(ctx context.Context, filter Filter) ([]Entry, error) {
	return p.primary.List(ctx, filter)
}

func (p *PrimaryFallbackProvider) FormatForPrompt(ctx context.Context, query string, limit int) (string, error) {
	entries, err := p.Search(ctx, query, limit)
	if err != nil {
		return "", err
	}
	return formatEntries(entries), nil
}

func (p *PrimaryFallbackProvider) Close() error {
	if err := p.primary.Close(); err != nil {
		return err
	}
	return p.fallback.Close()
}
