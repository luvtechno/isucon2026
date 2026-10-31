package isureal

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// 応答の JSON は頻度が高いので、手で組み立てる。

func appendJSONString(b []byte, s string) []byte {
	b = append(b, '"')
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if c >= 0x20 && c != '"' && c != '\\' && c != '<' && c != '>' && c != '&' {
				i++
				continue
			}
			b = append(b, s[start:i]...)
			switch c {
			case '"':
				b = append(b, '\\', '"')
			case '\\':
				b = append(b, '\\', '\\')
			case '\n':
				b = append(b, '\\', 'n')
			case '\r':
				b = append(b, '\\', 'r')
			case '\t':
				b = append(b, '\\', 't')
			default:
				b = append(b, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xf])
			}
			i++
			start = i
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b = append(b, s[start:i]...)
			b = append(b, `�`...)
			i += size
			start = i
			continue
		}
		if r == ' ' || r == ' ' {
			b = append(b, s[start:i]...)
			b = append(b, `\u202`...)
			b = append(b, hexDigits[r&0xf])
			i += size
			start = i
			continue
		}
		i += size
	}
	b = append(b, s[start:]...)
	return append(b, '"')
}

func appendTimeUS(b []byte, us int64) []byte {
	b = append(b, '"')
	b = time.UnixMicro(us).UTC().AppendFormat(b, time.RFC3339Nano)
	return append(b, '"')
}

// --- 画像の署名 ---

const imageURLLifetime = 24 * time.Hour
const imageSignatureIterations = 1000

// hmacSHA256 は鍵が32バイトで、本文が短い HMAC-SHA256 をアロケーションなしで計算する。
func hmacSHA256(key *[32]byte, msg []byte) [32]byte {
	var inner [64 + 64]byte
	for i := 0; i < 32; i++ {
		inner[i] = key[i] ^ 0x36
	}
	for i := 32; i < 64; i++ {
		inner[i] = 0x36
	}
	n := copy(inner[64:], msg)
	ih := sha256.Sum256(inner[:64+n])
	var outer [64 + 32]byte
	for i := 0; i < 32; i++ {
		outer[i] = key[i] ^ 0x5c
	}
	for i := 32; i < 64; i++ {
		outer[i] = 0x5c
	}
	copy(outer[64:], ih[:])
	return sha256.Sum256(outer[:])
}

func signatureMessage(dst []byte, exp int64, imageID ID) []byte {
	dst = strconv.AppendInt(dst, exp, 10)
	dst = append(dst, ':')
	return imageID.appendTo(dst)
}

// FullImageSignature は画像 URL の署名（HMAC の繰り返し）を計算する。重いので、なるべく覚えておいたものを使う。
func FullImageSignature(key *[32]byte, imageID ID, exp int64) [32]byte {
	var buf [64]byte
	v := hmacSHA256(key, signatureMessage(buf[:0], exp, imageID))
	for i := 1; i < imageSignatureIterations; i++ {
		v = hmacSHA256(key, v[:])
	}
	return v
}

type sigCacheKey struct {
	id  ID
	exp int64
}

// sigCache は表に無い組み合わせの署名を覚えておく。
var sigCache sync.Map

func cachedSignature(key *[32]byte, imageID ID, exp int64) [32]byte {
	k := sigCacheKey{imageID, exp}
	if v, ok := sigCache.Load(k); ok {
		return *v.(*[32]byte)
	}
	v := FullImageSignature(key, imageID, exp)
	sigCache.Store(k, &v)
	return v
}

// 署名の期限。過去の画像の署名は sigExp について前もって計算してある（Pack.PostSigs など）。
// 期限が近づいたら、その時点から24時間後へ切り替える。
var sigExp atomic.Int64

const minSigExpRemaining = time.Hour

// currentImageExp はいま発行する URL の期限を返す。
func currentImageExp(now time.Time) int64 {
	if exp := sigExp.Load(); exp-now.Unix() >= int64(minSigExpRemaining/time.Second) {
		return exp
	}
	return now.Add(imageURLLifetime).Unix()
}

type renderer struct {
	b      []byte
	exp    int64
	expStr string
	st     *State
}

func newRenderer(st *State, b []byte) *renderer {
	exp := currentImageExp(time.Now())
	return &renderer{b: b, exp: exp, expStr: strconv.FormatInt(exp, 10), st: st}
}

// signedURL は覚えてある署名（sig は sigAt を期限とするもの）を使って URL を書く。
func (r *renderer) signedURL(key *[32]byte, imageID ID, known *[32]byte, knownExp int64) {
	var sig [32]byte
	if known != nil && knownExp == r.exp {
		sig = *known
	} else {
		sig = cachedSignature(key, imageID, r.exp)
	}
	b := r.b
	b = append(b, `"/api/images/`...)
	b = imageID.appendTo(b)
	b = append(b, `?exp=`...)
	b = append(b, r.expStr...)
	b = append(b, `&sig=`...)
	n := len(b)
	b = append(b, make([]byte, 43)...)
	base64.RawURLEncoding.Encode(b[n:n+43], sig[:])
	r.b = append(b, '"')
}

func (r *renderer) str(s string) { r.b = appendJSONString(r.b, s) }
func (r *renderer) raw(s string) { r.b = append(r.b, s...) }

// user は relationshipUserResponse / postAuthorResponse の形（どちらも同じ並び）。
func (r *renderer) user(u *User, bio string) {
	r.raw(`{"id":"`)
	r.raw(u.IDStr)
	r.raw(`","username":`)
	r.str(u.Username)
	r.raw(`,"displayName":`)
	r.str(u.DisplayName)
	r.raw(`,"profileImageUrl":`)
	if u.HasProfile {
		r.signedURL(&u.Key, u.ProfileImg, &u.ProfileSig, u.ProfileSigExp)
	} else {
		r.raw(`null`)
	}
	r.raw(`,"bio":`)
	r.str(bio)
	r.raw(`,"accountType":"`)
	r.raw(u.accountType())
	r.raw(`"}`)
}

func (r *renderer) location(kind uint8, hasCoords bool, lat, lon float64, locality string) {
	r.raw(`{"kind":"`)
	r.raw(locationNames[kind])
	r.raw(`"`)
	if kind == LocExact && hasCoords {
		r.raw(`,"latitude":`)
		r.b = appendFloat(r.b, lat)
		r.raw(`,"longitude":`)
		r.b = appendFloat(r.b, lon)
	}
	if kind == LocApproximate {
		r.raw(`,"locality":`)
		r.str(locality)
	}
	r.raw(`}`)
}

func appendFloat(b []byte, f float64) []byte {
	// encoding/json と同じ表記
	v, err := json.Marshal(f)
	if err != nil {
		return strconv.AppendFloat(b, f, 'f', -1, 64)
	}
	return append(b, v...)
}

func (r *renderer) postTiming(postedAt, deadline int64) {
	r.raw(`,"postedAt":`)
	r.b = appendTimeUS(r.b, postedAt)
	if postedAt <= deadline {
		r.raw(`,"onTime":true,"lateSeconds":0`)
	} else {
		r.raw(`,"onTime":false,"lateSeconds":`)
		r.b = strconv.AppendInt(r.b, (postedAt-deadline)/1_000_000, 10)
	}
}

// post は createPostResponse の中身（閉じ括弧の手前まで）を書く。
func (r *renderer) post(p *Post) {
	a := p.Author
	r.raw(`{"id":"`)
	r.raw(p.IDStr)
	r.raw(`","author":`)
	r.user(a, a.Bio)
	r.raw(`,"postingWindowId":"`)
	r.raw(p.Window.IDStr)
	r.raw(`","imageUrl":`)
	r.signedURL(&a.Key, p.ImageID, &p.ImageSig, p.ImageSigExp)
	r.raw(`,"caption":`)
	r.str(p.Caption)
	r.raw(`,"visibility":"`)
	r.raw(visibilityNames[p.Vis])
	r.raw(`","location":`)
	r.location(p.Loc.Kind, p.Loc.HasCoords, p.Loc.Lat, p.Loc.Lon, p.Loc.Locality)
	r.postTiming(p.PostedAt, p.Window.OnTimeDeadline)
}

func (r *renderer) histPost(s *State, p *HistPost, idx uint32) {
	a := s.initUsers[p.Author]
	r.raw(`{"id":"`)
	r.b = p.ID.appendTo(r.b)
	r.raw(`","author":`)
	r.user(a, a.Bio)
	w := s.initWindow[p.Window]
	r.raw(`,"postingWindowId":"`)
	r.raw(w.IDStr)
	r.raw(`","imageUrl":`)
	r.signedURL(&a.Key, p.ImageID, &s.pack.PostSigs[idx], s.pack.SigExp)
	r.raw(`,"caption":`)
	r.b = appendJSONString(r.b, s.pack.str(p.CaptionOff, p.CaptionLen))
	r.raw(`,"visibility":"`)
	r.raw(p.Visibility())
	r.raw(`","location":`)
	kind := uint8(p.LocKind())
	r.location(kind, kind == LocExact, p.Lat, p.Lon, s.pack.str(p.LocalityOff, p.LocalityLen))
	r.postTiming(p.PostedAt, w.OnTimeDeadline)
}

func (r *renderer) realmoji(id ID, reactor *User, typ uint8, imageID ID, createdAt int64, sig *[32]byte, sigExp int64) {
	r.raw(`{"id":"`)
	r.b = id.appendTo(r.b)
	r.raw(`","reactor":`)
	r.user(reactor, reactor.Bio)
	r.raw(`,"reactionType":"`)
	r.raw(reactionTypeNames[typ])
	r.raw(`","imageUrl":`)
	r.signedURL(&reactor.Key, imageID, sig, sigExp)
	r.raw(`,"createdAt":`)
	r.b = appendTimeUS(r.b, createdAt)
	r.raw(`}`)
}

func (r *renderer) comment(id ID, author *User, body string, createdAt int64) {
	r.raw(`{"id":"`)
	r.b = id.appendTo(r.b)
	r.raw(`","author":`)
	r.user(author, author.Bio)
	r.raw(`,"body":`)
	r.str(body)
	r.raw(`,"createdAt":`)
	r.b = appendTimeUS(r.b, createdAt)
	r.raw(`}`)
}

func (r *renderer) sessionUser(u *User) {
	r.raw(`{"id":"`)
	r.raw(u.IDStr)
	r.raw(`","username":`)
	r.str(u.Username)
	r.raw(`,"displayName":`)
	r.str(u.DisplayName)
	r.raw(`,"bio":`)
	r.str(u.Bio)
	r.raw(`,"region":"`)
	r.raw(u.Region)
	r.raw(`","accountType":"`)
	r.raw(u.accountType())
	r.raw(`","status":"`)
	r.raw(u.status())
	r.raw(`"`)
}

// me は getMeResponse の形。
func (r *renderer) me(u *User) {
	r.sessionUser(u)
	r.raw(`,"profileImageUrl":`)
	if u.HasProfile {
		r.signedURL(&u.Key, u.ProfileImg, &u.ProfileSig, u.ProfileSigExp)
	} else {
		r.raw(`null`)
	}
	r.raw(`}`)
}
