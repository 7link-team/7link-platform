package control

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

type Profile struct {
	Server    string `json:"server"`
	TenantID  string `json:"tenant_id"`
	MachineID string `json:"machine_id"`
	ClientID  string `json:"client_id"`
	Secret    string `json:"client_secret"`
}

// Store holds an OS lock for its lifetime; process death releases the lock.
type Store struct {
	Dir     string
	release func()
}

func OpenStore(dir string) (*Store, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err = os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	release, err := lockProfile(filepath.Join(dir, "profile.lock"))
	if err != nil {
		return nil, errors.New("profile is occupied or cannot be locked")
	}
	return &Store{Dir: dir, release: release}, nil
}
func (s *Store) Close() {
	if s.release != nil {
		s.release()
		s.release = nil
	}
}

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// Write atomically replaces a private file after syncing its complete contents.
// Windows seals all profile contents to the current OS user with DPAPI.
func (s *Store) Write(name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data, err = protect(data)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.Dir, ".pending-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return replaceFile(f.Name(), filepath.Join(s.Dir, name))
}
func (s *Store) Read(name string, value any) error {
	path := filepath.Join(s.Dir, name)
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 16*1024*1024 {
		return errors.New("invalid profile file")
	}
	if err = privateFile(info); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 16*1024*1024+1))
	if err != nil {
		return err
	}
	data, err = unprotect(data)
	if err != nil {
		return errors.New("cannot unlock profile for this OS user")
	}
	if json.Unmarshal(data, value) != nil {
		return errors.New("invalid profile data")
	}
	return nil
}
func (s *Store) SaveProfile(p Profile) error { return s.Write("identity.json", p) }
func (s *Store) Profile() (Profile, error) {
	var p Profile
	err := s.Read("identity.json", &p)
	if err == nil && (!validID(p.TenantID) || !validID(p.MachineID) || !validID(p.ClientID) || p.Secret == "") {
		err = errors.New("incomplete profile")
	}
	return p, err
}

func validID(value string) bool {
	n, err := strconv.ParseInt(value, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == value
}
