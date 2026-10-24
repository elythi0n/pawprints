package sessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/elythi0n/pawprints/internal/store"
)

// Claude Code keeps the logged-in account in .claude.json under
// "oauthAccount": next to the config folder for ~/.claude, inside it when
// CLAUDE_CONFIG_DIR points elsewhere. The file also holds per-project state
// and grows to hundreds of kilobytes, so the answer is cached by size and
// modification time and a hook usually pays one stat.

type oauthAccount struct {
	AccountUUID       string `json:"accountUuid"`
	EmailAddress      string `json:"emailAddress"`
	OrganizationName  string `json:"organizationName"`
	OrganizationType  string `json:"organizationType"`
	UserRateLimitTier string `json:"userRateLimitTier"`
	BillingType       string `json:"billingType"`
}

type accountCache struct {
	Path    string        `json:"path"`
	Size    int64         `json:"size"`
	ModTime time.Time     `json:"mod_time"`
	Account store.Account `json:"account"`
}

// AccountFile is the .claude.json that holds a config folder's account.
func AccountFile(home string) string {
	if def, err := os.UserHomeDir(); err == nil && filepath.Clean(home) == filepath.Join(def, ".claude") {
		return filepath.Join(def, ".claude.json")
	}
	return filepath.Join(home, ".claude.json")
}

// CurrentAccount is the account the config folder is logged in to.
func CurrentAccount(home string) (store.Account, bool) {
	path := AccountFile(home)
	info, err := os.Stat(path)
	if err != nil {
		return store.Account{}, false
	}
	cachePath := filepath.Join(store.Home(), "state", "account-"+store.ProjectID(path)+".json")
	if data, err := os.ReadFile(cachePath); err == nil {
		var c accountCache
		if json.Unmarshal(data, &c) == nil && c.Path == path && c.Size == info.Size() && c.ModTime.Equal(info.ModTime()) {
			return c.Account, c.Account.ID != ""
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return store.Account{}, false
	}
	var file struct {
		OAuth *oauthAccount `json:"oauthAccount"`
	}
	if json.Unmarshal(data, &file) != nil {
		return store.Account{}, false
	}
	var acct store.Account
	if file.OAuth != nil {
		acct = store.Account{ID: file.OAuth.AccountUUID, Email: file.OAuth.EmailAddress, Org: file.OAuth.OrganizationName, Plan: PlanName(*file.OAuth)}
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err == nil {
		if out, err := json.Marshal(accountCache{Path: path, Size: info.Size(), ModTime: info.ModTime(), Account: acct}); err == nil {
			_ = os.WriteFile(cachePath, out, 0o600)
		}
	}
	return acct, acct.ID != ""
}

// PlanName is the plan as people call it: Team or Enterprise for an
// organization seat, else Max 20x, Max 5x, Pro, or Free from the user's tier.
func PlanName(a oauthAccount) string {
	switch {
	case strings.Contains(a.OrganizationType, "enterprise"):
		return "Enterprise"
	case strings.Contains(a.OrganizationType, "team"):
		return "Team"
	}
	tier := strings.ToLower(a.UserRateLimitTier)
	switch {
	case strings.Contains(tier, "max_20x"):
		return "Max 20x"
	case strings.Contains(tier, "max_5x"):
		return "Max 5x"
	case strings.Contains(tier, "max"):
		return "Max"
	case strings.Contains(tier, "pro"):
		return "Pro"
	case a.BillingType == "" && tier == "":
		return ""
	}
	return "Free"
}
