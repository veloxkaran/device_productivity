package hub

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Employer struct {
	UserID    int64
	Name      string
	Companies map[int64]EmployerCompany
}

type EmployerCompany struct {
	ID                int64   `json:"id"`
	Name              string  `json:"name"`
	IsOwner           bool    `json:"is_owner"`
	CanViewActivity   bool    `json:"can_view_activity"`
	ActivityMemberIDs []int64 `json:"activity_member_ids"`
	// Per-action grants from Laravel ("add/edit/delete device activity").
	// Absent (an older API) means owner-only, exactly the previous rule.
	CanAddActivity    bool `json:"can_add_activity"`
	CanEditActivity   bool `json:"can_edit_activity"`
	CanDeleteActivity bool `json:"can_delete_activity"`
}

func (c *EmployerCompany) UnmarshalJSON(b []byte) error {
	var raw struct {
		ID                int64   `json:"id"`
		Name              string  `json:"name"`
		IsOwner           bool    `json:"is_owner"`
		CanViewActivity   *bool   `json:"can_view_activity"`
		ActivityMemberIDs []int64 `json:"activity_member_ids"`
		CanAddActivity    *bool   `json:"can_add_activity"`
		CanEditActivity   *bool   `json:"can_edit_activity"`
		CanDeleteActivity *bool   `json:"can_delete_activity"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	c.ID, c.Name, c.IsOwner, c.ActivityMemberIDs = raw.ID, raw.Name, raw.IsOwner, raw.ActivityMemberIDs
	c.CanViewActivity = raw.IsOwner
	if raw.CanViewActivity != nil {
		c.CanViewActivity = *raw.CanViewActivity
	}
	pick := func(v *bool) bool {
		if v == nil {
			return raw.IsOwner
		}
		return *v
	}
	c.CanAddActivity = pick(raw.CanAddActivity)
	c.CanEditActivity = pick(raw.CanEditActivity)
	c.CanDeleteActivity = pick(raw.CanDeleteActivity)
	return nil
}

var (
	errSessionRejected     = errors.New("invalid or expired session")
	errActivityUnavailable = errors.New("activity service temporarily unavailable")
)

type EmployerVerifier interface {
	Verify(token string) (*Employer, error)
}

type cachedEmployer struct {
	employer *Employer
	expires  time.Time
}

type Verifier struct {
	apiURL string
	client *http.Client
	mu     sync.Mutex
	cache  map[string]cachedEmployer
}

func NewVerifier(apiURL string) *Verifier {
	return &Verifier{apiURL: apiURL, client: &http.Client{Timeout: 8 * time.Second}, cache: map[string]cachedEmployer{}}
}

func (v *Verifier) Verify(token string) (*Employer, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("%w: missing token", errSessionRejected)
	}
	key := hashToken(token)
	v.mu.Lock()
	if c, ok := v.cache[key]; ok && time.Now().Before(c.expires) {
		v.mu.Unlock()
		return c.employer, nil
	}
	v.mu.Unlock()

	req, _ := http.NewRequest(http.MethodGet, v.apiURL+"/chat/verify-token?include=activity", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: hajir api unreachable: %v", errActivityUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w: token rejected (%d)", errSessionRejected, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: hajir api returned %d", errActivityUnavailable, resp.StatusCode)
	}
	var body struct {
		Data struct {
			User struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"user"`
			Companies []EmployerCompany `json:"companies"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("%w: bad verify response: %v", errActivityUnavailable, err)
	}
	e := &Employer{UserID: body.Data.User.ID, Name: body.Data.User.Name, Companies: map[int64]EmployerCompany{}}
	for _, c := range body.Data.Companies {
		e.Companies[c.ID] = c
	}

	v.mu.Lock()
	v.cache[key] = cachedEmployer{employer: e, expires: time.Now().Add(time.Minute)}
	if len(v.cache) > 5000 {
		for k, c := range v.cache {
			if time.Now().After(c.expires) {
				delete(v.cache, k)
			}
		}
	}
	v.mu.Unlock()
	return e, nil
}

func (e *Employer) Owns(companyID int64) bool {
	c, ok := e.Companies[companyID]
	return ok && c.IsOwner
}

func (e *Employer) Access(companyID int64) (Scope, bool) {
	c, ok := e.Companies[companyID]
	if !ok || !c.CanViewActivity {
		return Scope{}, false
	}
	if c.IsOwner {
		return Scope{IsOwner: true, CanAdd: true, CanEdit: true, CanDelete: true}, true
	}
	grants := Scope{CanAdd: c.CanAddActivity, CanEdit: c.CanEditActivity, CanDelete: c.CanDeleteActivity}
	if c.ActivityMemberIDs == nil {
		return grants, true
	}
	ids := make(map[int64]bool, len(c.ActivityMemberIDs))
	for _, id := range c.ActivityMemberIDs {
		ids[id] = true
	}
	grants.MemberIDs = ids
	return grants, true
}
