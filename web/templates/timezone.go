package templates

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	appTimeLocationMu   sync.RWMutex
	appTimeLocation     = time.UTC
	appTimeLocationName = "UTC"
)

// SetAppTimezone configures the timezone used for all rendered timestamp text.
// Timezone values must be IANA timezone names (for example, "America/New_York").
func SetAppTimezone(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		trimmed = "UTC"
	}

	loc, err := time.LoadLocation(trimmed)
	if err != nil {
		return fmt.Errorf("load app timezone %q: %w", trimmed, err)
	}

	appTimeLocationMu.Lock()
	appTimeLocation = loc
	appTimeLocationName = trimmed
	appTimeLocationMu.Unlock()
	return nil
}

func appTimezoneLocation() *time.Location {
	appTimeLocationMu.RLock()
	loc := appTimeLocation
	appTimeLocationMu.RUnlock()
	return loc
}

func formatInAppTime(v any, layout string) string {
	switch t := v.(type) {
	case time.Time:
		return t.In(appTimezoneLocation()).Format(layout)
	case *time.Time:
		if t == nil {
			return ""
		}
		return t.In(appTimezoneLocation()).Format(layout)
	default:
		return ""
	}
}
