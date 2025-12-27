type QueryResult struct {
	Hash              string            `json:"hash"`
	SpotifyAppVersion string            `json:"spotifyAppVersion"`
	PayloadVersion    string            `json:"payloadVersion"`
	Headers           map[string]string `json:"headers,omitempty"`
	Payload           string            `json:"payload,omitempty"`
}
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