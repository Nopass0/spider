package store

import "time"

// Device — зарегистрированный клиент-агент.
type Device struct {
	DeviceID  string    `json:"device_id"`
	Name      string    `json:"name"`
	KeyB64    string    `json:"-"`          // никогда не отдаётся в API
	Hostname  string    `json:"hostname"`
	OS        string    `json:"os"`
	Arch      string    `json:"arch"`
	CPUBrand  string    `json:"cpu_brand"`
	CPUCores  int       `json:"cpu_cores"`
	MemTotal  uint64    `json:"mem_total"`
	AgentVer  string    `json:"agent_version"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Online    bool      `json:"online"`
}

// Enrollment — одноразовый токен регистрации, созданный админом.
type Enrollment struct {
	Token     string     `json:"token"`
	Note      string     `json:"note"`
	KeyB64    string     `json:"-"`      // не отдаётся в листинге; только при создании
	PubB64    string     `json:"pub_b64"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	UsedBy    string     `json:"used_by,omitempty"`
}

// CommandStatus — жизненный цикл команды.
type CommandStatus string

const (
	StatusQueued     CommandStatus = "queued"
	StatusRunning    CommandStatus = "running"
	StatusDone       CommandStatus = "done"
	StatusError      CommandStatus = "error"
	StatusTimeout    CommandStatus = "timeout"
	StatusCancelled  CommandStatus = "cancelled"
)

// Command — команда на выполнение на устройстве.
type Command struct {
	ID           string        `json:"id"`
	DeviceID     string        `json:"device_id"`
	Command      string        `json:"command"`
	TimeoutSec   int           `json:"timeout_sec"`
	Status       CommandStatus `json:"status"`
	CreatedAt    time.Time     `json:"created_at"`
	DispatchedAt *time.Time    `json:"dispatched_at,omitempty"`
	FinishedAt   *time.Time    `json:"finished_at,omitempty"`
	CreatedBy    string        `json:"created_by"`
}

// Result — результат выполнения команды.
type Result struct {
	CommandID  string    `json:"command_id"`
	ExitCode   int       `json:"exit_code"`
	StdoutB64  string    `json:"stdout_b64"`
	StderrB64  string    `json:"stderr_b64"`
	FinishedAt time.Time `json:"finished_at"`
	DurationMs int64     `json:"duration_ms"`
}

// AuditEntry — запись аудита.
type AuditEntry struct {
	ID     int64  `json:"id"`
	Actor  string `json:"actor"`
	Action string `json:"action"`
	Target string `json:"target"`
	Detail string `json:"detail"`
	At     int64  `json:"at"`
}
