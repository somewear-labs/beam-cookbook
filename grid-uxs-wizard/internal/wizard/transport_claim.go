package wizard

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type transportClaim struct {
	Code          string `json:"code"`
	Hostname      string `json:"hostname"`
	Backend       string `json:"backend"`
	WorkspaceID   string `json:"workspaceId"`
	GCSIdentityID string `json:"gcsIdentityId"`
	IdentityID    string `json:"identityId"`
}

var rawClaimPattern = regexp.MustCompile(`^uxs1\.[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}\.[A-Za-z0-9_-]{43}\.[a-f0-9]{64}$`)
var hostnamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,252}$`)
var backendPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$`)

func parseTransportClaim(value string) (transportClaim, error) {
	if rawClaimPattern.MatchString(value) {
		return transportClaim{Code: value}, nil
	}
	var result transportClaim
	if len(value) > 4096 || !strings.HasPrefix(value, "uxst1.") {
		return result, errors.New("invalid setup code")
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "uxst1."))
	if err != nil {
		return result, errors.New("invalid claim encoding")
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return transportClaim{}, errors.New("invalid claim data")
	}
	if !rawClaimPattern.MatchString(result.Code) || !hostnamePattern.MatchString(result.Hostname) || !backendPattern.MatchString(result.Backend) {
		return transportClaim{}, errors.New("invalid TVPN claim metadata")
	}
	for _, id := range []string{result.WorkspaceID, result.GCSIdentityID, result.IdentityID} {
		if !numericID.MatchString(id) {
			return transportClaim{}, errors.New("invalid claim identity or Workspace")
		}
	}
	if result.IdentityID == result.GCSIdentityID {
		return transportClaim{}, errors.New("UxS and GCS must have separate identities")
	}
	return result, nil
}

func encodeTransportClaim(value transportClaim) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	code := "uxst1." + base64.RawURLEncoding.EncodeToString(data)
	_, err = parseTransportClaim(code)
	return code, err
}

func readTransportClaim(path string) (transportClaim, error) {
	if err := checkCodeFile(path); err != nil {
		return transportClaim{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return transportClaim{}, err
	}
	return parseTransportClaim(strings.TrimSpace(string(data)))
}

func saveBootstrapKey(directory, id, key string) (string, error) {
	if !uuidPattern.MatchString(id) || !apiKeyPattern.MatchString(key) {
		return "", errors.New("invalid bootstrap credential")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("bootstrap credential directory must be private")
	}
	if err := secureBootstrapPath(directory); err != nil {
		return "", err
	}
	path := filepath.Join(directory, id+".api-key")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 512 {
			return "", errors.New("invalid private credential file")
		}
		if err := secureBootstrapPath(path); err != nil {
			return "", err
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != key {
			return "", errors.New("existing bootstrap credential differs")
		}
		return path, nil
	}
	if err != nil {
		return "", err
	}
	if err := secureBootstrapPath(path); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	_, writeErr := f.WriteString(key)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return "", errors.New("could not save the UxS credential")
	}
	return path, nil
}
