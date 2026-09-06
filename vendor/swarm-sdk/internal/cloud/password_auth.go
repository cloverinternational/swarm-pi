package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// PasswordAuthChallenge describes an auth challenge response.
type PasswordAuthChallenge struct {
	Name       string
	Session    string
	Parameters map[string]string
}

// PasswordAuthResult contains tokens or a challenge to resolve.
type PasswordAuthResult struct {
	Tokens    *TokenSet
	Challenge *PasswordAuthChallenge
}

// PasswordChallengeResponse supplies data to respond to a challenge.
type PasswordChallengeResponse struct {
	ChallengeName string
	Session       string
	Email         string
	Code          string
	NewPassword   string
	MFAType       string
	Answer        string
}

type cognitoAuthResult struct {
	AccessToken  string `json:"AccessToken"`
	IDToken      string `json:"IdToken"`
	RefreshToken string `json:"RefreshToken"`
	TokenType    string `json:"TokenType"`
	ExpiresIn    int64  `json:"ExpiresIn"`
}

type cognitoAuthResponse struct {
	ChallengeName        string             `json:"ChallengeName"`
	Session              string             `json:"Session"`
	ChallengeParameters  map[string]string  `json:"ChallengeParameters"`
	AuthenticationResult *cognitoAuthResult `json:"AuthenticationResult"`
	Raw                  map[string]any     `json:"-"`
}

type cognitoErrorResponse struct {
	Type    string `json:"__type"`
	Message string `json:"message"`
}

// InitiatePasswordAuth starts the USER_PASSWORD_AUTH flow.
func InitiatePasswordAuth(ctx context.Context, config *CloudConfig, email, password string) (*PasswordAuthResult, error) {
	if config == nil {
		return nil, fmt.Errorf("cloud config is required")
	}
	if strings.TrimSpace(email) == "" || strings.TrimSpace(password) == "" {
		return nil, fmt.Errorf("email and password are required")
	}
	if config.PublicClientID == "" {
		return nil, fmt.Errorf("public client id missing")
	}

	payload := map[string]any{
		"AuthFlow": "USER_PASSWORD_AUTH",
		"ClientId": config.PublicClientID,
		"AuthParameters": map[string]string{
			"USERNAME": strings.TrimSpace(email),
			"PASSWORD": password,
		},
	}

	response, err := executeCognitoRequest(ctx, config, "InitiateAuth", payload)
	if err != nil {
		return nil, err
	}

	return buildPasswordAuthResult(response), nil
}

// RespondToPasswordChallenge answers a Cognito auth challenge.
func RespondToPasswordChallenge(ctx context.Context, config *CloudConfig, input PasswordChallengeResponse) (*PasswordAuthResult, error) {
	if config == nil {
		return nil, fmt.Errorf("cloud config is required")
	}
	if config.PublicClientID == "" {
		return nil, fmt.Errorf("public client id missing")
	}
	if strings.TrimSpace(input.ChallengeName) == "" || strings.TrimSpace(input.Session) == "" {
		return nil, fmt.Errorf("challenge name and session are required")
	}
	if strings.TrimSpace(input.Email) == "" {
		return nil, fmt.Errorf("email is required")
	}

	challengeResponses := map[string]string{
		"USERNAME": strings.TrimSpace(input.Email),
	}

	switch input.ChallengeName {
	case "SMS_MFA":
		if strings.TrimSpace(input.Code) == "" {
			return nil, fmt.Errorf("verification code is required")
		}
		challengeResponses["SMS_MFA_CODE"] = strings.TrimSpace(input.Code)
	case "SOFTWARE_TOKEN_MFA":
		if strings.TrimSpace(input.Code) == "" {
			return nil, fmt.Errorf("verification code is required")
		}
		challengeResponses["SOFTWARE_TOKEN_MFA_CODE"] = strings.TrimSpace(input.Code)
	case "NEW_PASSWORD_REQUIRED":
		if strings.TrimSpace(input.NewPassword) == "" {
			return nil, fmt.Errorf("new password is required")
		}
		challengeResponses["NEW_PASSWORD"] = input.NewPassword
	case "SELECT_MFA_TYPE":
		if strings.TrimSpace(input.MFAType) == "" {
			return nil, fmt.Errorf("mfa type is required")
		}
		challengeResponses["ANSWER"] = strings.TrimSpace(input.MFAType)
	case "CUSTOM_CHALLENGE":
		if strings.TrimSpace(input.Answer) == "" {
			return nil, fmt.Errorf("challenge response is required")
		}
		challengeResponses["ANSWER"] = strings.TrimSpace(input.Answer)
	case "MFA_SETUP":
		return nil, fmt.Errorf("mfa setup is not supported in the IDE yet; complete setup on swarmcode.ai")
	default:
		return nil, fmt.Errorf("unsupported challenge: %s", input.ChallengeName)
	}

	payload := map[string]any{
		"ChallengeName":      input.ChallengeName,
		"ClientId":           config.PublicClientID,
		"Session":            input.Session,
		"ChallengeResponses": challengeResponses,
	}

	response, err := executeCognitoRequest(ctx, config, "RespondToAuthChallenge", payload)
	if err != nil {
		return nil, err
	}

	return buildPasswordAuthResult(response), nil
}

func buildPasswordAuthResult(response *cognitoAuthResponse) *PasswordAuthResult {
	if response == nil {
		return &PasswordAuthResult{}
	}
	if response.AuthenticationResult != nil && response.AuthenticationResult.AccessToken != "" {
		auth := response.AuthenticationResult
		var expiresAt int64
		if auth.ExpiresIn > 0 {
			expiresAt = time.Now().Add(time.Duration(auth.ExpiresIn) * time.Second).Unix()
		}
		return &PasswordAuthResult{
			Tokens: &TokenSet{
				AccessToken:  auth.AccessToken,
				IDToken:      auth.IDToken,
				RefreshToken: auth.RefreshToken,
				TokenType:    auth.TokenType,
				ExpiresAt:    expiresAt,
			},
		}
	}
	if response.ChallengeName != "" {
		return &PasswordAuthResult{
			Challenge: &PasswordAuthChallenge{
				Name:       response.ChallengeName,
				Session:    response.Session,
				Parameters: response.ChallengeParameters,
			},
		}
	}
	return &PasswordAuthResult{}
}

func executeCognitoRequest(ctx context.Context, config *CloudConfig, target string, payload any) (*cognitoAuthResponse, error) {
	region, err := resolveCognitoRegion(config)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("https://cognito-idp.%s.amazonaws.com/", region), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to build auth request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AWSCognitoIdentityProviderService."+target)

	client := http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cognito request failed: %w", err)
	}
	defer resp.Body.Close()

	var respBody bytes.Buffer
	if _, err := respBody.ReadFrom(resp.Body); err != nil {
		return nil, fmt.Errorf("failed to read cognito response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errPayload cognitoErrorResponse
		if err := json.Unmarshal(respBody.Bytes(), &errPayload); err == nil && errPayload.Message != "" {
			return nil, fmt.Errorf("cognito error: %s", errPayload.Message)
		}
		return nil, fmt.Errorf("cognito error: %s", strings.TrimSpace(respBody.String()))
	}

	var parsed cognitoAuthResponse
	var raw map[string]any
	if err := json.Unmarshal(respBody.Bytes(), &raw); err != nil {
		return nil, fmt.Errorf("failed to parse cognito response: %w", err)
	}
	if err := json.Unmarshal(respBody.Bytes(), &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse cognito response: %w", err)
	}
	parsed.Raw = raw

	return &parsed, nil
}

func resolveCognitoRegion(config *CloudConfig) (string, error) {
	if config == nil {
		return "", fmt.Errorf("cloud config is required")
	}
	if strings.TrimSpace(config.Region) != "" {
		return config.Region, nil
	}
	if strings.TrimSpace(config.AuthIssuerURL) == "" {
		return "", fmt.Errorf("cloud region missing")
	}

	parsed, err := url.Parse(config.AuthIssuerURL)
	if err != nil {
		return "", fmt.Errorf("invalid issuer url: %w", err)
	}
	hostParts := strings.Split(parsed.Hostname(), ".")
	if len(hostParts) < 2 || hostParts[0] != "cognito-idp" {
		return "", fmt.Errorf("invalid issuer host")
	}
	return hostParts[1], nil
}
