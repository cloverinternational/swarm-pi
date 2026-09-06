package bridge

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/chrome/protocol"
)

const nonceBytes = 32

func randomToken() (string, error) {
	raw := make([]byte, nonceBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func handshakeProof(secret []byte, role, installationID, clientNonce, serverNonce string, epoch uint64, instanceID string, selected protocol.Negotiated) string {
	mac := hmac.New(sha256.New, secret)
	selectedJSON, _ := json.Marshal(selected)
	fmt.Fprintf(mac, "swarm.chrome.bridge/1\x00%s\x00%s\x00%s\x00%s\x00%d\x00%s\x00%s",
		role, installationID, clientNonce, serverNonce, epoch, instanceID, selectedJSON)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func rebindProof(secret []byte, installationID, claimID string, generation uint64, windowID int64, challenge string) string {
	mac := hmac.New(sha256.New, secret)
	fmt.Fprintf(mac, "swarm.chrome.rebind/1\x00%s\x00%s\x00%d\x00%d\x00%s",
		installationID, claimID, generation, windowID, challenge)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func secureEqual(left, right string) bool {
	return hmac.Equal([]byte(left), []byte(right))
}
