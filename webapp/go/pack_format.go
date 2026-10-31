package isureal

import (
	"encoding/hex"
	"time"
)

// ID は UUID の16バイト表現。アプリの ID はすべて UUID の文字列なので、この形で持つ。
type ID [16]byte

var ZeroID ID

func (id ID) IsZero() bool { return id == ZeroID }

func (id ID) String() string {
	var b [36]byte
	id.appendTo(b[:0])
	return string(b[:])
}

const hexDigits = "0123456789abcdef"

func (id ID) appendTo(dst []byte) []byte {
	for i, c := range id {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			dst = append(dst, '-')
		}
		dst = append(dst, hexDigits[c>>4], hexDigits[c&0xf])
	}
	return dst
}

// ParseID は小文字の UUID 文字列だけを受け付ける（DB の文字列比較と同じ結果にするため）。
func ParseID(s string) (ID, bool) {
	var id ID
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return id, false
	}
	j := 0
	for i := 0; i < 36; {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			i++
			continue
		}
		a, b := hexVal(s[i]), hexVal(s[i+1])
		if a < 0 || b < 0 {
			return id, false
		}
		id[j] = byte(a<<4 | b)
		j++
		i += 2
	}
	return id, true
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	}
	return -1
}

func mustID(s string) ID {
	id, ok := ParseID(s)
	if !ok {
		panic("invalid id: " + s)
	}
	return id
}

func decodeHexKey(s string) []byte {
	b, _ := hex.DecodeString(s)
	return b
}

// 過去データの固定長レコード。mmap したファイルをそのまま読むので、ポインタを持たない。

const (
	PostVisFriends = 0
	PostVisFoF     = 1
	PostVisPublic  = 2

	LocNone        = 0
	LocExact       = 1
	LocApproximate = 2

	histFlagDeleted = 1 << 8
)

var visibilityNames = [...]string{"friends", "friends_of_friends", "public"}
var locationNames = [...]string{"none", "exact", "approximate"}
var reactionTypeNames = [...]string{"like", "smile", "surprise", "heart", "laugh", "instant"}

// ReactionTypeIndex は realmoji の種類の番号を返す。
func ReactionTypeIndex(name string) uint8 {
	for i, n := range reactionTypeNames {
		if n == name {
			return uint8(i)
		}
	}
	return 0
}

type HistPost struct {
	ID          ID
	ImageID     ID
	PostedAt    int64 // マイクロ秒
	Lat, Lon    float64
	RMStart     uint32
	RMEnd       uint32
	CStart      uint32
	CEnd        uint32
	Author      uint32
	Window      uint32
	CaptionOff  uint32
	LocalityOff uint32
	CaptionLen  uint16
	LocalityLen uint16
	LocalDate   int32  // 1970-01-01 からの日数
	Flags       uint32 // 下位4ビット: 公開範囲、次の4ビット: 位置の種類（bit 4..7）、bit 8: 削除済み
}

func (p *HistPost) Visibility() string  { return visibilityNames[p.Flags&0xf] }
func (p *HistPost) LocKind() uint32     { return (p.Flags >> 4) & 0xf }
func (p *HistPost) DeletedInPack() bool { return p.Flags&histFlagDeleted != 0 }

type HistRM struct {
	ID        ID
	ImageID   ID
	CreatedAt int64
	Post      uint32
	Reactor   uint32
	Type      uint8
	Replaced  uint8
	_         [6]byte
}

type HistComment struct {
	ID        ID
	CreatedAt int64
	Post      uint32
	Author    uint32
	BodyOff   uint32
	BodyLen   uint16
	Deleted   uint16
}

type IDIndex struct {
	ID  ID
	Idx uint32
}

const (
	ImageKindProfile  = 0
	ImageKindPost     = 1
	ImageKindRealmoji = 2
)

type HistImage struct {
	ID    ID
	Off   uint64
	Len   uint32
	CRC   uint32
	Owner uint32
	Ref   uint32
	Kind  uint8
	_     [7]byte
}

// InitialData は初期状態の users などの小さな表。起動時と初期化で、ここから状態を組み立てる。
type InitialData struct {
	Users          []InitUser          `json:"users"`
	Friendships    []InitFriendship    `json:"friendships"`
	Follows        []InitFollow        `json:"follows"`
	Blocks         []InitBlock         `json:"blocks"`
	FriendRequests []InitFriendRequest `json:"friendRequests"`
	Windows        []InitWindow        `json:"windows"`
	Admins         []InitAdmin         `json:"admins"`
	PrefixLen      uint64              `json:"prefixLen"` // images.dat のうち、europe/america が持つ先頭部分の長さ
	NumPosts       int                 `json:"numPosts"`
}

type InitUser struct {
	ID             string  `json:"id"`
	Username       string  `json:"username"`
	SecretKeyHash  string  `json:"secretKeyHash"`
	DisplayName    string  `json:"displayName"`
	Bio            string  `json:"bio"`
	ProfileImageID *string `json:"profileImageId,omitempty"`
	Region         string  `json:"region"`
	AccountType    string  `json:"accountType"`
	Status         string  `json:"status"`
	PostStart      uint32  `json:"postStart"`
	PostEnd        uint32  `json:"postEnd"`
}

type InitFriendship struct {
	ID        string    `json:"id"`
	Low       string    `json:"low"`
	High      string    `json:"high"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type InitFollow struct {
	ID        string    `json:"id"`
	Follower  string    `json:"follower"`
	Followee  string    `json:"followee"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type InitBlock struct {
	ID      string `json:"id"`
	Blocker string `json:"blocker"`
	Blocked string `json:"blocked"`
}

type InitFriendRequest struct {
	ID        string    `json:"id"`
	Requester string    `json:"requester"`
	Recipient string    `json:"recipient"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type InitWindow struct {
	ID             string     `json:"id"`
	Region         string     `json:"region"`
	LocalDate      int32      `json:"localDate"`
	StartsAt       time.Time  `json:"startsAt"`
	OnTimeDeadline time.Time  `json:"onTimeDeadline"`
	EndedAt        *time.Time `json:"endedAt,omitempty"`
}

type InitAdmin struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"passwordHash"`
}

func dateToDays(t time.Time) int32 {
	y, m, d := t.Date()
	return int32(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

func daysToDate(days int32) time.Time {
	return time.Unix(int64(days)*86400, 0).UTC()
}
