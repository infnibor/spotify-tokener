package internal
type SpotifyToken struct {
	AccessToken                      string `json:"accessToken"`
	AccessTokenExpirationTimestampMs int64  `json:"accessTokenExpirationTimestampMs"`
	ClientId                         string `json:"clientId"`
	IsAnonymous                      bool   `json:"isAnonymous"`
}

type Cookie struct {
	Name  string
	Value string
}