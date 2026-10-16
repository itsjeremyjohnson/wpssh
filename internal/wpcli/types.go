package wpcli

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// FlexString is a string field that wp-cli sometimes emits as a bool or null.
// wp plugin list reports "update": false on must-use and dropin rows.
// It decodes a string verbatim, false as "none", true as "available", and
// null as "". It always encodes as a JSON string.
type FlexString string

// UnmarshalJSON implements json.Unmarshaler.
func (f *FlexString) UnmarshalJSON(data []byte) error {
	switch string(bytes.TrimSpace(data)) {
	case "null":
		*f = ""
		return nil
	case "false":
		*f = "none"
		return nil
	case "true":
		*f = "available"
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("want string, bool, or null, got %s", data)
	}
	*f = FlexString(s)
	return nil
}

// MarshalJSON implements json.Marshaler.
func (f FlexString) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(f))
}

// Plugin represents a WordPress plugin as returned by wp plugin list --format=json.
type Plugin struct {
	Name          string     `json:"name"`
	Status        string     `json:"status"` // active, inactive, must-use, dropin
	Update        FlexString `json:"update"` // available, none
	Version       string     `json:"version"`
	UpdateVersion string     `json:"update_version,omitempty"`
	AutoUpdate    string     `json:"auto_update"` // on, off
}

// Theme represents a WordPress theme as returned by wp theme list --format=json.
type Theme struct {
	Name       string     `json:"name"`
	Status     string     `json:"status"` // active, inactive, parent
	Update     FlexString `json:"update"`
	Version    string     `json:"version"`
	AutoUpdate string     `json:"auto_update"`
}

// User represents a WordPress user as returned by wp user list --format=json.
type User struct {
	ID             int    `json:"ID"`
	UserLogin      string `json:"user_login"`
	DisplayName    string `json:"display_name"`
	UserEmail      string `json:"user_email"`
	Roles          string `json:"roles"` // comma-separated
	UserRegistered string `json:"user_registered"`
}

// Post represents a WordPress post as returned by wp post list --format=json.
type Post struct {
	ID         int    `json:"ID"`
	PostTitle  string `json:"post_title"`
	PostName   string `json:"post_name"`
	PostStatus string `json:"post_status"`
	PostType   string `json:"post_type"`
	PostDate   string `json:"post_date"`
}

// Option represents a WordPress option with its value (for live queries).
type Option struct {
	OptionName  string `json:"option_name"`
	OptionValue string `json:"option_value,omitempty"` // Only in live queries, never cached
	Autoload    string `json:"autoload"`
}

// OptionMeta represents option metadata without values (safe for caching).
type OptionMeta struct {
	OptionName string `json:"option_name"`
	Autoload   string `json:"autoload"`
}

// CronEvent represents a WordPress cron event.
type CronEvent struct {
	Hook       string `json:"hook"`
	NextRun    string `json:"next_run_relative"`
	Recurrence string `json:"recurrence"`
}

// CronSchedule represents a WordPress cron schedule.
type CronSchedule struct {
	Name     string `json:"name"`
	Interval int    `json:"interval"`
	Display  string `json:"display"`
}

// CoreVersion holds the WordPress core version string.
type CoreVersion struct {
	Version string // parsed from wp core version output
}

// CoreUpdate represents an available WordPress core update.
type CoreUpdate struct {
	Version    string `json:"version"`
	UpdateType string `json:"update_type"` // major, minor
	PackageURL string `json:"package_url"`
}
