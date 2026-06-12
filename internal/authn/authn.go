// Package authn provides authentication information binding to the request.
package authn

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

type ID string

func (id ID) String() string {
	return string(id)
}

const NoAuthn ID = ""

func Enable() bool {
	return Default != nil
}

func extractEntry(a *Authenticator, r *http.Request) *Entry {
	if a == nil {
		return nil
	}
	s := r.Header.Get("Authorization")
	return a.index[s]
}

type entryKey struct{}

// withEntry creates and returns a context.Context to which the Entry is bound.
func withEntry(ctx context.Context, entry *Entry) context.Context {
	return context.WithValue(ctx, entryKey{}, entry)
}

func WrapHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Embed authenticity information to request context.
		entry := extractEntry(Default, r)
		if entry != nil {
			ctx := withEntry(r.Context(), entry)
			r = r.WithContext(ctx)
		}
		h.ServeHTTP(w, r)
	})
}

func AuthnEntry(ctx context.Context) (*Entry, bool) {
	entry, ok := ctx.Value(entryKey{}).(*Entry)
	return entry, ok
}

func AuthnID(ctx context.Context) (ID, bool) {
	entry, ok := AuthnEntry(ctx)
	if !ok {
		return NoAuthn, false
	}
	return entry.ID, true
}

type Type string

const (
	Basic  Type = "basic"
	Bearer Type = "bearer"
)

type User struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

type Entry struct {
	ID   ID   `json:"id"`
	Type Type `json:"type"`

	// Used for Basic type
	User *User `json:"user,omitempty"`

	// Used for Bearer type
	Token *string `json:"token,omitempty"`

	InitQuery string `json:"init_query,omitempty"`
}

func (e *Entry) headerValue() string {
	switch e.Type {
	case Basic:
		if e.User != nil {
			return "Basic " + base64.StdEncoding.EncodeToString([]byte(e.User.Name+":"+e.User.Password))
		}
	case Bearer:
		if e.Token != nil {
			return "Bearer " + strings.TrimSpace(*e.Token)
		}
	}
	return ""
}

func (e *Entry) validate() error {
	switch e.Type {
	case Basic:
		if e.User == nil {
			return errors.New("required \"user\" property for \"basic\" type")
		}
		if e.User.Name == "" {
			return errors.New("required \"user.name\" property for \"basic\" type")
		}
		if e.User.Password == "" {
			return errors.New("required \"user.password\" property for \"basic\" type")
		}
	case Bearer:
		if e.Token == nil {
			return errors.New("required \"token\" property for \"bearer\" type")
		}
		if *e.Token == "" {
			return errors.New("required non-empty \"token\" for \"bearer\" type")
		}
	}
	return nil
}

var (
	Default *Authenticator
)

func ReadFile(name string) error {
	var err error
	Default, err = LoadFile(name)
	return err
}

type Authenticator struct {
	entries []Entry
	index   map[string]*Entry
}

func LoadFile(name string) (*Authenticator, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	a, err := readAuthenticator(f)
	if err != nil {
		return nil, err
	}
	return a, nil
}

func readAuthenticator(r io.Reader) (*Authenticator, error) {
	var entries []Entry
	err := json.NewDecoder(r).Decode(&entries)
	if err != nil {
		return nil, err
	}
	idmap := map[ID]struct{}{}
	index := map[string]*Entry{}
	for i := range entries {
		e := &entries[i]
		// 1. Check for duplicate IDs.
		if _, ok := idmap[e.ID]; ok {
			return nil, fmt.Errorf("duplicated ID: %s", e.ID)
		}
		idmap[e.ID] = struct{}{}
		// 2. Check the type and validate required fields.
		err := e.validate()
		if err != nil {
			return nil, err
		}
		// 3. Create a reverse lookup index.
		x := e.headerValue()
		if x == "" {
			continue
		}
		index[x] = e
	}
	return &Authenticator{
		entries: entries,
		index:   index,
	}, nil
}

func (a *Authenticator) AuthenticateHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Embed authenticity information to request context.
		entry := extractEntry(a, r)
		if entry != nil {
			ctx := withEntry(r.Context(), entry)
			r = r.WithContext(ctx)
		}
		h.ServeHTTP(w, r)
	})
}
