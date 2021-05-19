package main

import (
	"errors"
	"fmt"
	"github.com/gofrs/uuid"
	"github.com/gorilla/schema"
	"github.com/gorilla/securecookie"
	"net/http"
)

type cookies struct {
	Login string
}

// Cookies is cookie name enum
var Cookies = cookies{
	Login: "login",
}

// TODO rolling keys, see the github page
var cookieHashKey = []byte("fpl4b11zfpl4b11zfpl4b11zfpl4b11z")  // TODO persist and generate securely
var cookieBlockKey = []byte("fpl4b11zfpl4b11zfpl4b11zfpl4b11z") // TODO persist and generate securely
var cookieOven = securecookie.New(cookieHashKey, cookieBlockKey)
var decoder = schema.NewDecoder()

// AuthToken is AuthToken
type AuthToken struct {
	Secret string
	UserID string
}

func CreateAuthToken(userID string) (*AuthToken, error) {
	s, err := uuid.NewV4()
	if err != nil {
		return nil, err
	}
	return &AuthToken{
		Secret: s.String(),
		UserID: userID,
	}, nil
}

// ParseAuthToken parses map into token
func ParseAuthToken(value map[string]string) (*AuthToken, error) {
	secret, ok := value["secret"]
	if !ok {
		return nil, fmt.Errorf("missing secret")
	}
	userID, ok := value["userID"]
	if !ok {
		return nil, fmt.Errorf("missing userid")
	}
	return &AuthToken{
		Secret: secret,
		UserID: userID,
	}, nil
}

func mapAuthToken(token *AuthToken) map[string]string {
	return map[string]string{"secret": token.Secret, "userID": token.UserID}
}

// SetSecureCookie sets cookie
func SetSecureCookie(w http.ResponseWriter, name string, value map[string]string) error {
	encoded, err := cookieOven.Encode(name, value)
	if err != nil {
		return err
	}
	cookie := &http.Cookie{
		Name:     name,
		Value:    encoded,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
	}
	http.SetCookie(w, cookie)
	return nil
}

// UnsetCookie unsets cookie
func UnsetCookie(w http.ResponseWriter, name string) {
	cookie := &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		Secure:   true,
		MaxAge:   -1,
		HttpOnly: true,
	}
	http.SetCookie(w, cookie)
}

// GetSecureCookie gets cookie
func GetSecureCookie(r *http.Request, name string) (map[string]string, error) {
	cookie, err := r.Cookie(name)
	if err != nil {
		return nil, err
	}
	value := make(map[string]string)
	if err := cookieOven.Decode(name, cookie.Value, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func (a *App) GetUserIDFromCookie(r *http.Request) (string, error) {
	cookieMap, err := GetSecureCookie(r, Cookies.Login)
	if errors.Is(err, http.ErrNoCookie) {
		return "", nil
	}
	if err != nil {
		return "", err
	}

	token, err := ParseAuthToken(cookieMap)
	if err != nil {
		return "", err
	}

	return token.UserID, nil
}
