package memory

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// RetryableProvider wraps a MemoryProvider with retry logic.
type RetryableProvider struct {
	inner      MemoryProvider
	maxRetries int
	baseDelay  time.Duration
	maxDelay   time.Duration
}

// NewRetryableProvider creates a new retryable provider.
func NewRetryableProvider(inner MemoryProvider, maxRetries int) *RetryableProvider {
	return &RetryableProvider{
		inner:      inner,
		maxRetries: maxRetries,
		baseDelay:  1 * time.Second,
		maxDelay:   30 * time.Second,
	}
}

func (p *RetryableProvider) Store(ctx context.Context, entry Entry) error {
	return p.retry(ctx, func() error {
		return p.inner.Store(ctx, entry)
	})
}

func (p *RetryableProvider) Search(ctx context.Context, query string, limit int) ([]Entry, error) {
	var result []Entry
	err := p.retry(ctx, func() error {
		var err error
		result, err = p.inner.Search(ctx, query, limit)
		return err
	})
	return result, err
}

func (p *RetryableProvider) Get(ctx context.Context, key string) (Entry, bool, error) {
	var result Entry
	var found bool
	err := p.retry(ctx, func() error {
		var err error
		result, found, err = p.inner.Get(ctx, key)
		return err
	})
	return result, found, err
}

func (p *RetryableProvider) Delete(ctx context.Context, key string) error {
	return p.retry(ctx, func() error {
		return p.inner.Delete(ctx, key)
	})
}

func (p *RetryableProvider) List(ctx context.Context, filter Filter) ([]Entry, error) {
	var result []Entry
	err := p.retry(ctx, func() error {
		var err error
		result, err = p.inner.List(ctx, filter)
		return err
	})
	return result, err
}

func (p *RetryableProvider) FormatForPrompt(ctx context.Context, query string, limit int) (string, error) {
	var result string
	err := p.retry(ctx, func() error {
		var err error
		result, err = p.inner.FormatForPrompt(ctx, query, limit)
		return err
	})
	return result, err
}

func (p *RetryableProvider) Close() error {
	return p.inner.Close()
}

func (p *RetryableProvider) retry(ctx context.Context, fn func() error) error {
	var lastErr error
	for attempt := 0; attempt <= p.maxRetries; attempt++ {
		if attempt > 0 {
			delay := p.baseDelay * time.Duration(1<<uint(attempt-1))
			if delay > p.maxDelay {
				delay = p.maxDelay
			}
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		err := fn()
		if err == nil {
			return nil
		}

		lastErr = err

		// Check if retryable
		var memErr *MemoryError
		if errors.As(err, &memErr) && !memErr.IsRetryable() {
			return err
		}
	}
	return fmt.Errorf("max retries exceeded: %w", lastErr)
}
