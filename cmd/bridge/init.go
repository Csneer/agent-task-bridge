package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Csneer/agent-task-bridge/bridge"
	"golang.org/x/sys/unix"
)

type bootstrap struct {
	AuthorityID    string `json:"authority_id"`
	Owner          string `json:"owner_principal_id"`
	AgentPrincipal string `json:"agent_principal_id"`
	Agent          string `json:"agent_id"`
	Reader         string `json:"reader_principal_id"`
	NotBefore      string `json:"not_before"`
	ExpiresAt      string `json:"expires_at"`
	Config         string `json:"admin_config"`
	Credentials    string `json:"credentials_file"`
}

func writePrivate(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func initialize(args []string, output io.Writer) (err error) {
	fs := flag.NewFlagSet("bridge init", flag.ContinueOnError)
	dirArg := fs.String("dir", "", "new private directory (parent must exist; never overwritten)")
	validity := fs.Duration("valid-for", 30*24*time.Hour, "credential and binding lifetime, >0 and <=8760h; rotate with new instance IDs before expiry")
	if err = fs.Parse(args); err != nil {
		return err
	}
	if *dirArg == "" || fs.NArg() != 0 {
		return errors.New("usage: bridge init --dir NEW_PRIVATE_DIRECTORY [--valid-for 720h]")
	}
	if *validity <= 0 || *validity > 365*24*time.Hour {
		return errors.New("valid-for must be positive and at most 8760h")
	}
	dir, err := filepath.Abs(*dirArg)
	if err != nil {
		return err
	}
	target := dir
	if _, e := os.Lstat(target); e == nil || !os.IsNotExist(e) {
		return errors.New("initialization target already exists or cannot be inspected; nothing overwritten")
	}
	dir, err = os.MkdirTemp(filepath.Dir(target), ".bridge-init-")
	if err != nil {
		return err
	}
	// Staging is never a usable target; failed setup leaves a named private
	// directory for inspection, never recursively deleting user paths.
	installed := false
	defer func() {
		if err != nil && installed {
			err = fmt.Errorf("authority installed in %s, but completion reporting or directory sync failed; preserve it and inspect before reuse: %w", target, err)
			return
		}
		if err != nil {
			err = fmt.Errorf("initialization failed; inspect and manually remove staging directory %s: %w", dir, err)
		}
	}()
	if err = os.Chmod(dir, 0700); err != nil {
		return err
	}
	random := func() (string, error) {
		b := make([]byte, 32)
		_, e := rand.Read(b)
		return base64.RawURLEncoding.EncodeToString(b), e
	}
	id, err := random()
	if err != nil {
		return err
	}
	idDigest := sha256.Sum256([]byte(id))
	now := time.Now().UTC().Truncate(time.Millisecond)
	info := bootstrap{AuthorityID: "authority_" + hex.EncodeToString(idDigest[:16]), Owner: "principal_owner", AgentPrincipal: "principal_agent", Agent: "agent_local", Reader: "principal_reader", NotBefore: now.Format("2006-01-02T15:04:05.000Z"), ExpiresAt: now.Add(*validity).Format("2006-01-02T15:04:05.000Z"), Config: "admin.json", Credentials: "credentials.json"}
	cfg := bridge.Config{AuthorityID: info.AuthorityID, Agents: []bridge.Agent{{AgentID: info.Agent, Enabled: true}}, OwnerCapabilities: []bridge.OwnerCapability{}, Bindings: []bridge.Binding{{BindingID: "binding_agent", PrincipalID: info.AgentPrincipal, AgentID: info.Agent, NotBefore: info.NotBefore, ExpiresAt: info.ExpiresAt}}}
	tokens := map[string]string{}
	for role, principal := range map[string]string{"owner": info.Owner, "agent": info.AgentPrincipal, "reader": info.Reader} {
		token, e := random()
		if e != nil {
			return e
		}
		tokens[role] = token
		digest := sha256.Sum256([]byte(token))
		cfg.Principals = append(cfg.Principals, bridge.Principal{PrincipalID: principal, Enabled: true})
		cfg.Credentials = append(cfg.Credentials, bridge.Credential{CredentialID: "credential_" + role, PrincipalID: principal, TokenHash: hex.EncodeToString(digest[:]), NotBefore: info.NotBefore, ExpiresAt: info.ExpiresAt})
	}
	if err = writePrivate(filepath.Join(dir, info.Config), cfg); err != nil {
		return err
	}
	if err = writePrivate(filepath.Join(dir, info.Credentials), tokens); err != nil {
		return err
	}
	key, err := loadKey(filepath.Join(dir, "bridge.key"), true)
	if err != nil {
		return err
	}
	service, err := bridge.Open(filepath.Join(dir, "bridge.sqlite"), &cfg, bridge.Options{Key: key})
	if err != nil {
		return err
	}
	if err = service.Close(); err != nil {
		return err
	}
	if err = writePrivate(filepath.Join(dir, "bootstrap.json"), info); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	d.Close()
	if err != nil {
		return err
	}
	// Linux is the supported daemon platform. NOREPLACE is essential: plain
	// rename can replace an existing empty directory created by another init.
	if err = unix.Renameat2(unix.AT_FDCWD, dir, unix.AT_FDCWD, target, unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	installed = true
	dir = target
	parent, err := os.Open(filepath.Dir(target))
	if err != nil {
		return err
	}
	err = parent.Sync()
	parent.Close()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "Initialized private authority in %s\nBootstrap: %s\nCredentials and binding expire at %s; rotate before expiry using new instance IDs and a full admin configuration import.\nStart: bridge --db %q --key %q\n", dir, filepath.Join(dir, "bootstrap.json"), info.ExpiresAt, filepath.Join(dir, "bridge.sqlite"), filepath.Join(dir, "bridge.key"))
	return err
}
