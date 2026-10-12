// Package systemd embeds binboat's systemd unit so the binary can install
// the exact file that ships in this directory (`binboat setup-host`).
package systemd

import _ "embed"

// Unit is contrib/systemd/binboat.service.
//
//go:embed binboat.service
var Unit string

// UnitPath is where the unit is installed.
const UnitPath = "/etc/systemd/system/binboat.service"
