package desktopusage

import "time"

// ScopedLimit contains a model-specific weekly limit from an account's response.
type ScopedLimit struct {
	Name                     string
	Percent                  *float64
	ResetAt                  *time.Time
	AllowancePercentOfWeekly *float64
}

// ResetGrant is a free-reset entitlement with no credential or grant identifier.
type ResetGrant struct {
	// Kind is ResetFull for a reset that clears the weekly limit, ResetModel for one that gives back a
	// model's share of it, or empty when the response does not say.
	Kind string
	// Model names the model a ResetModel grant gives back, when the response names one of its limits.
	Model     string
	Remaining int
	ExpiresAt time.Time
	Paused    bool
}

// Reset grant kinds.
const (
	ResetFull  = "full"
	ResetModel = "model"
)

// UsageRecord contains utilization, reset times and the response timestamp.
type UsageRecord struct {
	FiveHour         *float64
	SevenDay         *float64
	FiveHourReset    *time.Time
	SevenDayReset    *time.Time
	Scoped           []ScopedLimit
	ResetGrants      []ResetGrant
	ResetGrantsAt    time.Time
	ResetGrantsKnown bool
	UpdatedAt        time.Time
}
