//go:build integration

package integration

import (
	"testing"
)

func TestIntegrationSmoke(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration smoke test skipped in short mode")
	}
}