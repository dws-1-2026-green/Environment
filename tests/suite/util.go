package suite

import "github.com/google/uuid"

// randToken returns a short unique token for source/event naming.
func randToken() string { return uuid.NewString()[:8] }
