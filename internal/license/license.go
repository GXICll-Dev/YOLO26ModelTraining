package license

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Status struct {
	Valid       bool   `json:"valid"`
	Message     string `json:"message"`
	DaysLeft    int    `json:"daysLeft"`
	MachineCode string `json:"machineCode"`
	Mode        string `json:"mode"`
}

type Record struct {
	Key         string `json:"key"`
	ExpireDate  string `json:"expireDate"`
	ActivatedAt string `json:"activatedAt"`
}

type Manager struct {
	StateDir string
	Secret   string
}

func NewManager(stateDir string) Manager {
	return Manager{
		StateDir: stateDir,
		Secret:   os.Getenv("MT_LICENSE_SECRET"),
	}
}

func (m Manager) MachineCode() string {
	host, _ := os.Hostname()
	mac := firstMACAsUint()
	raw := fmt.Sprintf("%s-%d", host, mac)
	sum := md5Compat(raw)
	return strings.ToUpper(sum[:16])
}

func (m Manager) Check() Status {
	machine := m.MachineCode()
	if strings.EqualFold(os.Getenv("MT_LICENSE_MODE"), "dev") || m.Secret == "" {
		return Status{
			Valid:       true,
			Message:     "developer mode, no local license secret configured",
			DaysLeft:    9999,
			MachineCode: machine,
			Mode:        "dev",
		}
	}
	if ok, msg := m.checkTime(); !ok {
		return Status{Valid: false, Message: msg, MachineCode: machine, Mode: "offline"}
	}
	record, err := m.Load()
	if err != nil {
		return Status{Valid: false, Message: err.Error(), MachineCode: machine, Mode: "offline"}
	}
	if record.Key == "" {
		return Status{Valid: false, Message: "not activated", MachineCode: machine, Mode: "offline"}
	}
	daysLeft, err := m.Verify(record.Key)
	if err != nil {
		return Status{Valid: false, Message: err.Error(), MachineCode: machine, Mode: "offline"}
	}
	return Status{
		Valid:       true,
		Message:     "activated",
		DaysLeft:    daysLeft,
		MachineCode: machine,
		Mode:        "offline",
	}
}

func (m Manager) Activate(key string) (Status, error) {
	if m.Secret == "" {
		return m.Check(), errors.New("MT_LICENSE_SECRET is not configured")
	}
	daysLeft, err := m.Verify(key)
	if err != nil {
		return m.Check(), err
	}
	parts := strings.Split(strings.TrimSpace(key), "-")
	record := Record{
		Key:         strings.TrimSpace(key),
		ExpireDate:  parts[1],
		ActivatedAt: time.Now().Format("2006-01-02 15:04:05"),
	}
	if err := os.MkdirAll(m.StateDir, 0o700); err != nil {
		return m.Check(), err
	}
	data, _ := json.MarshalIndent(record, "", "  ")
	if err := os.WriteFile(filepath.Join(m.StateDir, "training_license.json"), data, 0o600); err != nil {
		return m.Check(), err
	}
	status := m.Check()
	status.DaysLeft = daysLeft
	return status, nil
}

func (m Manager) Load() (Record, error) {
	data, err := os.ReadFile(filepath.Join(m.StateDir, "training_license.json"))
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, nil
	}
	if err != nil {
		return Record{}, err
	}
	var record Record
	return record, json.Unmarshal(data, &record)
}

func (m Manager) Verify(key string) (int, error) {
	key = strings.TrimSpace(key)
	parts := strings.Split(key, "-")
	if len(parts) != 3 {
		return 0, errors.New("activation key must have 3 parts")
	}
	prefix, expireDate, hashPart := parts[0], parts[1], parts[2]
	if prefix != m.MachineCode()[:8] {
		return 0, errors.New("activation key does not match this machine")
	}
	raw := prefix + expireDate + m.Secret
	sum := sha256.Sum256([]byte(raw))
	expected := strings.ToUpper(hex.EncodeToString(sum[:])[:16])
	if !strings.EqualFold(expected, hashPart) {
		return 0, errors.New("activation key signature is invalid")
	}
	expire, err := time.ParseInLocation("20060102", expireDate, time.Local)
	if err != nil {
		return 0, errors.New("expiration date is invalid")
	}
	daysLeft := int(time.Until(expire).Hours() / 24)
	if time.Now().After(expire) {
		return daysLeft, errors.New("activation key has expired")
	}
	return daysLeft, nil
}

func (m Manager) checkTime() (bool, string) {
	if err := os.MkdirAll(m.StateDir, 0o700); err != nil {
		return false, err.Error()
	}
	path := filepath.Join(m.StateDir, "last_check.dat")
	now := time.Now().Unix()
	data, err := os.ReadFile(path)
	if err == nil {
		last, parseErr := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
		if parseErr == nil {
			if now < last-3600 {
				return false, "system time moved backward"
			}
			if now > last+30*24*3600 {
				return false, "system time jumped too far"
			}
		}
	}
	_ = os.WriteFile(path, []byte(strconv.FormatInt(now, 10)), 0o600)
	return true, "ok"
}

func firstMACAsUint() uint64 {
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || len(iface.HardwareAddr) == 0 {
			continue
		}
		var value uint64
		for _, b := range iface.HardwareAddr {
			value = value<<8 + uint64(b)
		}
		return value
	}
	return 0
}

func md5Compat(raw string) string {
	// Kept isolated for legacy compatibility with the Python machine-code format.
	h := legacyMD5([]byte(raw))
	return strings.ToUpper(hex.EncodeToString(h[:]))
}
