package cloud

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"net/url"
	"sort"
	"strings"
	"time"
)

const srpNHex string = "" +
	"FFFFFFFFFFFFFFFFC90FDAA22168C234C4C6628B80DC1CD1" +
	"29024E088A67CC74020BBEA63B139B22514A08798E3404DD" +
	"EF9519B3CD3A431B302B0A6DF25F14374FE1356D6D51C245" +
	"E485B576625E7EC6F44C42E9A637ED6B0BFF5CB6F406B7ED" +
	"EE386BFB5A899FA5AE9F24117C4B1FE649286651ECE45B3D" +
	"C2007CB8A163BF0598DA48361C55D39A69163FA8FD24CF5F" +
	"83655D23DCA3AD961C62F356208552BB9ED529077096966D" +
	"670C354E4ABC9804F1746C08CA18217C32905E462E36CE3B" +
	"E39E772C180E86039B2783A2EC07A28FB5C55DF06F4C52C9" +
	"DE2BCBF6955817183995497CEA956AE515D2261898FA0510" +
	"15728E5A8AAAC42DAD33170D04507A33A85521ABDF1CBA64" +
	"ECFB850458DBEF0A8AEA71575D060C7DB3970F85A6E1E4C7" +
	"ABF5AE8CDB0933D71E8C94E04A25619DCEE3D2261AD2EE6B" +
	"F12FFA06D98A0864D87602733EC86A64521F2B18177B200C" +
	"BBE117577A615D6C770988C0BAD946E208E24FA074E5AB31" +
	"43DB5BFCE0FD108E4B82D120A93AD2CAFFFFFFFFFFFFFFFF"

type srpHelper struct {
	poolName string
	n        *big.Int
	g        *big.Int
	k        *big.Int
	smallA   *big.Int
	largeA   *big.Int
}

func newSrpHelper(poolName string) (*srpHelper, error) {
	if strings.TrimSpace(poolName) == "" {
		return nil, fmt.Errorf("pool name is required")
	}

	n := new(big.Int)
	if _, ok := n.SetString(srpNHex, 16); !ok {
		return nil, fmt.Errorf("failed to parse SRP modulus")
	}
	g := big.NewInt(2)

	helper := &srpHelper{
		poolName: poolName,
		n:        n,
		g:        g,
	}

	nHex, err := padHex(n)
	if err != nil {
		return nil, err
	}
	gHex, err := padHex(g)
	if err != nil {
		return nil, err
	}
	kHex, err := helper.hexHash(nHex + gHex)
	if err != nil {
		return nil, err
	}
	k := new(big.Int)
	if _, ok := k.SetString(kHex, 16); !ok {
		return nil, fmt.Errorf("failed to parse SRP multiplier")
	}
	helper.k = k

	smallA, err := generateRandomSmallA()
	if err != nil {
		return nil, err
	}
	helper.smallA = smallA

	largeA, err := helper.calculateA(smallA)
	if err != nil {
		return nil, err
	}
	helper.largeA = largeA
	return helper, nil
}

func generateRandomSmallA() (*big.Int, error) {
	buffer := make([]byte, 128)
	if _, err := rand.Read(buffer); err != nil {
		return nil, fmt.Errorf("failed to generate SRP secret: %w", err)
	}
	value := new(big.Int).SetBytes(buffer)
	return value, nil
}

func (h *srpHelper) calculateA(a *big.Int) (*big.Int, error) {
	if a == nil {
		return nil, fmt.Errorf("srp secret is required")
	}
	largeA := new(big.Int).Exp(h.g, a, h.n)
	if new(big.Int).Mod(largeA, h.n).Sign() == 0 {
		return nil, fmt.Errorf("invalid SRP A value")
	}
	return largeA, nil
}

func (h *srpHelper) calculateU(largeA, serverB *big.Int) (*big.Int, error) {
	if largeA == nil || serverB == nil {
		return nil, fmt.Errorf("SRP values missing")
	}
	largeAHex, err := padHex(largeA)
	if err != nil {
		return nil, err
	}
	serverBHex, err := padHex(serverB)
	if err != nil {
		return nil, err
	}
	uHex, err := h.hexHash(largeAHex + serverBHex)
	if err != nil {
		return nil, err
	}
	u := new(big.Int)
	if _, ok := u.SetString(uHex, 16); !ok {
		return nil, fmt.Errorf("failed to parse SRP U")
	}
	return u, nil
}

func (h *srpHelper) getPasswordAuthenticationKey(username, password string, serverB, salt *big.Int) ([]byte, error) {
	if serverB == nil || salt == nil {
		return nil, fmt.Errorf("SRP challenge missing")
	}
	if strings.TrimSpace(username) == "" || strings.TrimSpace(password) == "" {
		return nil, fmt.Errorf("username and password are required")
	}

	if new(big.Int).Mod(serverB, h.n).Sign() == 0 {
		return nil, fmt.Errorf("invalid SRP B value")
	}

	u, err := h.calculateU(h.largeA, serverB)
	if err != nil {
		return nil, err
	}
	if u.Sign() == 0 {
		return nil, fmt.Errorf("invalid SRP U value")
	}

	usernamePassword := fmt.Sprintf("%s%s:%s", h.poolName, username, password)
	usernamePasswordHash := h.hashBytes([]byte(usernamePassword))
	saltHex, err := padHex(salt)
	if err != nil {
		return nil, err
	}
	xHex, err := h.hexHash(saltHex + usernamePasswordHash)
	if err != nil {
		return nil, err
	}
	xValue := new(big.Int)
	if _, ok := xValue.SetString(xHex, 16); !ok {
		return nil, fmt.Errorf("failed to parse SRP X")
	}

	gModPowX := new(big.Int).Exp(h.g, xValue, h.n)
	intValue2 := new(big.Int).Sub(serverB, new(big.Int).Mul(h.k, gModPowX))
	intValue2.Mod(intValue2, h.n)

	exponent := new(big.Int).Add(h.smallA, new(big.Int).Mul(u, xValue))
	sValue := new(big.Int).Exp(intValue2, exponent, h.n)

	sHex, err := padHex(sValue)
	if err != nil {
		return nil, err
	}
	uHex, err := padHex(u)
	if err != nil {
		return nil, err
	}
	sBytes, err := hex.DecodeString(sHex)
	if err != nil {
		return nil, fmt.Errorf("failed to decode SRP S: %w", err)
	}
	uBytes, err := hex.DecodeString(uHex)
	if err != nil {
		return nil, fmt.Errorf("failed to decode SRP U: %w", err)
	}
	return computeHKDF(sBytes, uBytes), nil
}

func (h *srpHelper) hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}

func (h *srpHelper) hexHash(hexStr string) (string, error) {
	data, err := hex.DecodeString(hexStr)
	if err != nil {
		return "", fmt.Errorf("invalid hex data: %w", err)
	}
	return h.hashBytes(data), nil
}

func padHex(value *big.Int) (string, error) {
	if value == nil {
		return "", fmt.Errorf("SRP value is nil")
	}

	if value.Sign() < 0 {
		return "", fmt.Errorf("SRP value must be positive")
	}

	hexStr := fmt.Sprintf("%x", value)
	if len(hexStr)%2 == 1 {
		hexStr = "0" + hexStr
	}
	if len(hexStr) > 0 {
		first := hexStr[0]
		if (first >= '8' && first <= '9') || (first >= 'a' && first <= 'f') || (first >= 'A' && first <= 'F') {
			hexStr = "00" + hexStr
		}
	}
	return hexStr, nil
}

func computeHKDF(ikm, salt []byte) []byte {
	info := append([]byte("Caldera Derived Key"), 0x01)
	prk := hmacSHA256(salt, ikm)
	okm := hmacSHA256(prk, info)
	if len(okm) < 16 {
		return okm
	}
	return okm[:16]
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func formatSrpTimestamp(now time.Time) string {
	weekdays := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	months := []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
	utc := now.UTC()
	return fmt.Sprintf(
		"%s %s %d %02d:%02d:%02d UTC %d",
		weekdays[utc.Weekday()],
		months[int(utc.Month())-1],
		utc.Day(),
		utc.Hour(),
		utc.Minute(),
		utc.Second(),
		utc.Year(),
	)
}

func resolveUserPoolName(config *CloudConfig) (string, error) {
	if config == nil {
		return "", fmt.Errorf("cloud config is required")
	}
	if strings.TrimSpace(config.AuthIssuerURL) == "" {
		return "", fmt.Errorf("auth issuer url missing")
	}
	parsed, err := url.Parse(config.AuthIssuerURL)
	if err != nil {
		return "", fmt.Errorf("invalid auth issuer url: %w", err)
	}
	poolID := strings.Trim(parsed.Path, "/")
	if poolID == "" {
		return "", fmt.Errorf("auth issuer url missing pool id")
	}
	parts := strings.Split(poolID, "_")
	if len(parts) < 2 {
		return "", fmt.Errorf("auth issuer url missing pool name")
	}
	return parts[1], nil
}

func summarizeSrpParams(params map[string]string) string {
	if len(params) == 0 {
		return ""
	}
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func summarizeRawKeys(values map[string]any) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// InitiateSrpAuth starts the USER_SRP_AUTH flow and completes the SRP exchange.
func InitiateSrpAuth(ctx context.Context, config *CloudConfig, email, password string) (*PasswordAuthResult, error) {
	if config == nil {
		return nil, fmt.Errorf("cloud config is required")
	}
	if strings.TrimSpace(email) == "" || strings.TrimSpace(password) == "" {
		return nil, fmt.Errorf("email and password are required")
	}
	if config.PublicClientID == "" {
		return nil, fmt.Errorf("public client id missing")
	}

	poolName, err := resolveUserPoolName(config)
	if err != nil {
		return nil, err
	}

	helper, err := newSrpHelper(poolName)
	if err != nil {
		return nil, err
	}

	payload := map[string]any{
		"AuthFlow": "USER_SRP_AUTH",
		"ClientId": config.PublicClientID,
		"AuthParameters": map[string]string{
			"USERNAME": strings.TrimSpace(email),
			"SRP_A":    fmt.Sprintf("%x", helper.largeA),
		},
	}

	response, err := executeCognitoRequest(ctx, config, "InitiateAuth", payload)
	if err != nil {
		return nil, err
	}

	if response.AuthenticationResult != nil && response.AuthenticationResult.AccessToken != "" {
		return buildPasswordAuthResult(response), nil
	}

	if response.ChallengeName != "PASSWORD_VERIFIER" {
		return buildPasswordAuthResult(response), nil
	}

	params := response.ChallengeParameters
	if params == nil {
		return nil, fmt.Errorf("missing SRP challenge parameters")
	}

	session := strings.TrimSpace(response.Session)
	if session == "" {
		session = strings.TrimSpace(params["Session"])
	}
	if session == "" {
		session = strings.TrimSpace(params["SESSION"])
	}
	if session == "" {
		sessionType := "missing"
		if rawSession, ok := response.Raw["Session"]; ok {
			sessionType = fmt.Sprintf("%T", rawSession)
			if value, ok := rawSession.(string); ok {
				sessionType = fmt.Sprintf("string(len=%d)", len(strings.TrimSpace(value)))
			}
		}
		log.Printf(
			"SRP initiate missing session: challenge=%s params=[%s] rawKeys=[%s] session=%s",
			response.ChallengeName,
			summarizeSrpParams(params),
			summarizeRawKeys(response.Raw),
			sessionType,
		)
	}

	userID := strings.TrimSpace(params["USER_ID_FOR_SRP"])
	if userID == "" {
		userID = strings.TrimSpace(email)
	}

	srpBHex := strings.TrimSpace(params["SRP_B"])
	saltHex := strings.TrimSpace(params["SALT"])
	secretBlock := strings.TrimSpace(params["SECRET_BLOCK"])
	if srpBHex == "" || saltHex == "" || secretBlock == "" {
		return nil, fmt.Errorf("invalid SRP challenge parameters")
	}

	serverB := new(big.Int)
	if _, ok := serverB.SetString(srpBHex, 16); !ok {
		return nil, fmt.Errorf("invalid SRP B value")
	}

	salt := new(big.Int)
	if _, ok := salt.SetString(saltHex, 16); !ok {
		return nil, fmt.Errorf("invalid SRP salt value")
	}

	hkdf, err := helper.getPasswordAuthenticationKey(userID, password, serverB, salt)
	if err != nil {
		return nil, err
	}

	secretBlockBytes, err := base64.StdEncoding.DecodeString(secretBlock)
	if err != nil {
		return nil, fmt.Errorf("invalid SRP secret block: %w", err)
	}

	timestamp := formatSrpTimestamp(time.Now())
	message := bytes.NewBuffer(nil)
	message.WriteString(poolName)
	message.WriteString(userID)
	message.Write(secretBlockBytes)
	message.WriteString(timestamp)

	signatureBytes := hmacSHA256(hkdf, message.Bytes())
	signature := base64.StdEncoding.EncodeToString(signatureBytes)

	challengeResponses := map[string]string{
		"USERNAME":                    userID,
		"PASSWORD_CLAIM_SECRET_BLOCK": secretBlock,
		"TIMESTAMP":                   timestamp,
		"PASSWORD_CLAIM_SIGNATURE":    signature,
	}

	respondPayload := map[string]any{
		"ChallengeName":      "PASSWORD_VERIFIER",
		"ClientId":           config.PublicClientID,
		"ChallengeResponses": challengeResponses,
	}
	if session != "" {
		respondPayload["Session"] = session
	}

	verifyResponse, err := executeCognitoRequest(ctx, config, "RespondToAuthChallenge", respondPayload)
	if err != nil {
		return nil, err
	}

	return buildPasswordAuthResult(verifyResponse), nil
}
