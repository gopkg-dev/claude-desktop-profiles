// Package profiles manages independent Claude Desktop data directories.
package profiles

import (
	"context"
	"errors"
	"sync"
	"time"

	"claudeprofiles/desktopusage"
)

var (
	ErrNotManaged          = errors.New("尚未接管 Claude Desktop，请先保存当前账号")
	ErrRecoveryRequired    = errors.New("上次切换尚未完成，请先恢复")
	ErrIdentityMismatch    = errors.New("账号目录中的登录与登记不一致，已停止操作")
	ErrDuplicateAccount    = errors.New("这个 Claude 账号已经保存在其他档案中")
	ErrNotFound            = errors.New("找不到账号档案")
	ErrActiveProfile       = errors.New("不能归档当前使用的账号，请先切到其他账号")
	ErrUnsafePath          = errors.New("账号数据路径不安全或被符号链接替换")
	ErrInvalidState        = errors.New("账号管理记录损坏，已停止操作")
	ErrNotSignedIn         = errors.New("Claude Desktop 尚未登录，请先完成登录")
	ErrAlreadyManaged      = errors.New("当前 Claude Desktop 已经纳入管理")
	ErrInvalidLabel        = errors.New("请输入 1 至 60 个字符的账号备注")
	ErrSessionOrganization = errors.New("尚未取得目标账号的组织信息，请先完成登录并在线刷新账号")
	ErrNoSessionSources    = errors.New("没有其他账号的已核实 Code 会话资料，请先登录来源账号并刷新资料")
	ErrWorkspaceRecovery   = errors.New("本地 Code 会话未能完整恢复，已暂停账号操作；请保留 backups/workspaces 备份并人工核对恢复")
)

// Paths separates Claude Desktop's real directory from this app's metadata.
type Paths struct {
	LiveDir        string
	StoreDir       string
	TranscriptRoot string
	// RequestLogDir holds the raw HTTP exchanges of online queries.
	RequestLogDir string
}

// Driver controls Desktop without accessing its credentials.
type Driver interface {
	Stop(context.Context) error
	Start(context.Context) error
}

// Profile describes a locally saved Desktop environment.
type Profile struct {
	ID                 string    `json:"id"`
	Label              string    `json:"label"`
	AccountID          string    `json:"accountId"`
	Active             bool      `json:"active"`
	NeedsLogin         bool      `json:"needsLogin"`
	NextRefreshAt      time.Time `json:"-"`
	RemoteUsageRetryAt time.Time `json:"-"`
	// Usage and its status are restored from this app's online display cache.
	Usage       *Usage `json:"-"`
	UsageStatus string `json:"-"`
	// RemoteUsageError retains the last explicit online refresh failure for this account.
	RemoteUsageError string `json:"-"`
	// Details contains verified online display metadata for this profile.
	Details       *AccountDetails `json:"-"`
	DetailsStatus string          `json:"-"`
}

// AccountDetails retains only the display fields of a matching account query.
type AccountDetails struct {
	Source           string
	Email            string
	DisplayName      string
	FullName         string
	CreatedAt        *time.Time
	OrganizationName string
	OrganizationID   string
	Plan             string
	UpdatedAt        time.Time
	Subscription     *SubscriptionDetails
}

// SubscriptionDetails contains org-scoped billing information provided by the account endpoint.
type SubscriptionDetails struct {
	BillingType     string
	BillingInterval string
	Status          string
	CreatedAt       *time.Time
	NextChargeAt    *time.Time
	NextChargeDate  string
	PlanEndingAt    *time.Time
	UpdatedAt       time.Time
}

const (
	DetailsAvailable       = "available"
	DetailsMissing         = "missing"
	DetailsInvalid         = "invalid"
	DetailsUnavailable     = "unavailable"
	DetailsMismatch        = "mismatch"
	DetailsPendingRecovery = "pendingRecovery"
)

// Usage describes one observed sample; percentages are used, not remaining.
type Usage struct {
	FiveHour         *float64
	SevenDay         *float64
	FiveHourReset    *time.Time
	SevenDayReset    *time.Time
	Scoped           []ScopedUsage
	ScopedUpdatedAt  time.Time
	ResetGrants      []ResetGrant
	ResetGrantsAt    time.Time
	ResetGrantsKnown bool
	UpdatedAt        time.Time
	Source           string
}

// ResetGrant describes one free reset entitlement returned online.
type ResetGrant struct {
	// Kind is desktopusage.ResetFull, desktopusage.ResetModel or empty when unknown; Model names a model reset's model.
	Kind      string
	Model     string
	Remaining int
	ExpiresAt time.Time
	Paused    bool
}

// ScopedUsage describes a model-specific weekly allowance.
type ScopedUsage struct {
	Name                     string
	Percent                  *float64
	ResetAt                  *time.Time
	AllowancePercentOfWeekly *float64
}

// UsageEntry updates only the usage presentation for one stable profile ID.
type UsageEntry struct {
	ID                 string
	Usage              *Usage
	Status             string
	RemoteUsageError   string
	NextRefreshAt      time.Time
	RemoteUsageRetryAt time.Time
}

// UsageSnapshot lets a UI refresh usage presentation without querying the network.
type UsageSnapshot struct {
	ActiveID        string
	Entries         []UsageEntry
	NeedsRefresh    bool
	PendingRecovery bool
}

const (
	UsageAvailable       = "available"
	UsageMissing         = "missing"
	UsageInvalid         = "invalid"
	UsageUnavailable     = "unavailable"
	UsagePendingRecovery = "pendingRecovery"
	UsageSourceRemote    = "remote"
)

// Snapshot contains only account metadata and operation status.
type Snapshot struct {
	Profiles        []Profile `json:"profiles"`
	Preview         bool      `json:"preview,omitempty"`
	ActiveID        string    `json:"activeId"`
	CurrentAccount  string    `json:"currentAccount"`
	PendingRecovery bool      `json:"pendingRecovery"`
	SharedSessions  bool      `json:"sharedSessions"`
	RecoveryMessage string    `json:"recoveryMessage,omitempty"`
}

type manifest struct {
	Version        int       `json:"version"`
	ActiveID       string    `json:"activeId"`
	Profiles       []Profile `json:"profiles"`
	SharedSessions bool      `json:"sharedSessions"`
	// SharingPausedOn is the account that was active when session sharing was paused. It is cleared
	// whenever the active account changes, so while it still names the active account, no other
	// account has run since the last merge and sharing can resume without one.
	SharingPausedOn string `json:"sharingPausedOn,omitempty"`
}

// activate makes id the active account, which voids a pause that could have resumed without a merge.
func (s *manifest) activate(id string) {
	s.ActiveID = id
	s.SharingPausedOn = ""
}

type journal struct {
	Kind   string `json:"kind,omitempty"`
	From   string `json:"from"`
	To     string `json:"to"`
	Backup string `json:"backup,omitempty"`
}

// Manager serializes directory changes and records recoverable transitions.
type Manager struct {
	paths  Paths
	driver Driver
	// step is used only by tests to simulate interrupted writes.
	step func(string) error
	// detailsReader lets isolated tests supply already-validated display metadata.
	detailsReader func(string, string, time.Time) (*AccountDetails, string)
	remoteMu      sync.Mutex
	accountQuery  AccountQuerier
	exitQuerier   func(context.Context, desktopusage.ExitInfo) (desktopusage.ExitInfo, error)
	// workspaceSync is replaced only by isolated integration tests.
	workspaceSync workspaceSyncer
	// workspaceRecovery also blocks this process if writing the durable marker fails.
	workspaceRecovery *journal
}

// New creates a manager without accessing any files or starting Desktop.
func New(paths Paths, driver Driver) *Manager {
	return &Manager{paths: paths, driver: driver}
}
