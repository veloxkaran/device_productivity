package hub

import (
	"encoding/json"
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
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	IsOwner bool   `json:"is_owner"`
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
		return nil, fmt.Errorf("missing token")
	}
	key := hashToken(token)
	v.mu.Lock()
	if c, ok := v.cache[key]; ok && time.Now().Before(c.expires) {
		v.mu.Unlock()
		return c.employer, nil
	}
	v.mu.Unlock()

	req, _ := http.NewRequest(http.MethodGet, v.apiURL+"/chat/verify-token", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hajir api unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token rejected (%d)", resp.StatusCode)
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
		return nil, fmt.Errorf("bad verify response: %w", err)
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
