// Package workspaces imports local Desktop work without sharing account credentials.
package workspaces

import "errors"

var (
	ErrUnsafePath      = errors.New("会话同步遇到不安全的路径或符号链接")
	ErrLimit           = errors.New("会话同步超过单次文件数量或大小限制")
	ErrInvalidIdentity = errors.New("会话同步缺少有效的账号和组织标识")
	ErrIO              = errors.New("会话文件读写失败；原始档案保持不变")
	ErrRollback        = errors.New("会话同步回滚未完成，请保留备份目录并恢复后再启动 Claude")
)

// Identity selects the account and organization partition, never a login secret.
type Identity struct {
	AccountID      string
	OrganizationID string
}

// Source describes an offline profile and its shared CLI transcript directory.
type Source struct {
	Dir            string
	TranscriptRoot string
	Identity       Identity
	// LiveDir is the canonical userData path after the profile is activated.
	LiveDir string
}

// Report contains counts and fixed reason codes, without titles, identifiers or paths.
type Report struct {
	CodeCopied  int            `json:"codeCopied"`
	CodeUpdated int            `json:"codeUpdated"`
	Tombstoned  int            `json:"tombstoned"`
	Skipped     int            `json:"skipped"`
	Reasons     map[string]int `json:"reasons,omitempty"`
}

func (r *Report) skip(reason string) {
	r.Skipped++
	if r.Reasons == nil {
		r.Reasons = make(map[string]int)
	}
	r.Reasons[reason]++
}

const (
	maxIndexBytes int64 = 10 << 20
	maxFileBytes  int64 = 256 << 20
	maxTotalBytes int64 = 2 << 30
	maxFiles            = 50000
	maxIndices          = 10000
)
