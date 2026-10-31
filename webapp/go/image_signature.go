package isureal

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"sync"
	"time"
)

const imageURLLifetime = 24 * time.Hour
const imageSignatureIterations = 1000

func imageSigningKey(secretKeyHash string) ([]byte, error) {
	key, err := hex.DecodeString(secretKeyHash)
	if err != nil || len(key) != sha256.Size {
		return nil, fmt.Errorf("invalid image signing key")
	}
	return key, nil
}

func computeImageSignature(key []byte, imageID string, exp int64) []byte {
	mac := hmac.New(sha256.New, key)
	value := make([]byte, 0, sha256.Size)
	mac.Write([]byte(strconv.FormatInt(exp, 10) + ":" + imageID))
	value = mac.Sum(value)
	for range imageSignatureIterations - 1 {
		mac.Reset()
		mac.Write(value)
		value = mac.Sum(value[:0])
	}
	return value
}

// 署名は (鍵, 画像, 期限) だけで決まるので覚えておく。期限を丸めるので、同じ画像の署名は丸めの幅のあいだ同じになる。
const imageExpGranularity = 60

type imageSigCacheKey struct {
	imageID string
	exp     int64
	key     string
}

var imageSigCache sync.Map // imageSigCacheKey -> []byte

func imageSignature(key []byte, imageID string, exp int64) []byte {
	cacheKey := imageSigCacheKey{imageID: imageID, exp: exp, key: string(key)}
	if cached, ok := imageSigCache.Load(cacheKey); ok {
		return cached.([]byte)
	}
	value := computeImageSignature(key, imageID, exp)
	imageSigCache.Store(cacheKey, value)
	return value
}

func signedImageURL(user User, imageID string) string {
	exp := time.Now().Add(imageURLLifetime).Unix()
	if rem := exp % imageExpGranularity; rem != 0 {
		exp += imageExpGranularity - rem
	}
	sig := base64.RawURLEncoding.EncodeToString(imageSignature(user.ImageKey, imageID, exp))
	return "/api/images/" + url.PathEscape(imageID) + "?exp=" + strconv.FormatInt(exp, 10) + "&sig=" + sig
}
