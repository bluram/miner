package main

import (
	"context"
	"time"
)

// commandTimeout bounds every external tool run, as the acceptance requires.
func commandTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 60*time.Second)
}
