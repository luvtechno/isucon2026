package isureal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"
	"unsafe"
)

// Pack は初期データの過去分を mmap したもの。読み取り専用で、全ノードが同じ中身を持つ
// （images.dat だけ、europe/america は先頭部分しか持たない）。
type Pack struct {
	Init        *InitialData
	Posts       []HistPost
	RMs         []HistRM
	Comments    []HistComment
	Images      []HistImage
	PostIDs     []IDIndex
	RMIDs       []IDIndex
	CommentIDs  []IDIndex
	Strings     []byte
	imageFile   *os.File
	imageFileSz uint64

	// 前もって計算した署名。SigExp がその期限（表が無ければ 0）。
	SigExp   int64
	PostSigs [][32]byte
	RMSigs   [][32]byte
	UserSigs [][32]byte
}

func mmapFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() == 0 {
		return nil, nil
	}
	return syscall.Mmap(int(f.Fd()), 0, int(st.Size()), syscall.PROT_READ, syscall.MAP_SHARED)
}

func mapSlice[T any](path string) ([]T, error) {
	b, err := mmapFile(path)
	if err != nil {
		return nil, err
	}
	var zero T
	size := int(unsafe.Sizeof(zero))
	if len(b)%size != 0 {
		return nil, fmt.Errorf("%s: size %d is not a multiple of %d", path, len(b), size)
	}
	if len(b) == 0 {
		return nil, nil
	}
	return unsafe.Slice((*T)(unsafe.Pointer(&b[0])), len(b)/size), nil
}

func OpenPack(dir string) (*Pack, error) {
	p := &Pack{}
	raw, err := os.ReadFile(filepath.Join(dir, "initial.json"))
	if err != nil {
		return nil, err
	}
	p.Init = &InitialData{}
	if err := json.Unmarshal(raw, p.Init); err != nil {
		return nil, err
	}
	if p.Posts, err = mapSlice[HistPost](filepath.Join(dir, "posts.bin")); err != nil {
		return nil, err
	}
	if p.RMs, err = mapSlice[HistRM](filepath.Join(dir, "realmojis.bin")); err != nil {
		return nil, err
	}
	if p.Comments, err = mapSlice[HistComment](filepath.Join(dir, "comments.bin")); err != nil {
		return nil, err
	}
	if p.Images, err = mapSlice[HistImage](filepath.Join(dir, "images.idx")); err != nil {
		return nil, err
	}
	if p.PostIDs, err = mapSlice[IDIndex](filepath.Join(dir, "post_ids.bin")); err != nil {
		return nil, err
	}
	if p.RMIDs, err = mapSlice[IDIndex](filepath.Join(dir, "realmoji_ids.bin")); err != nil {
		return nil, err
	}
	if p.CommentIDs, err = mapSlice[IDIndex](filepath.Join(dir, "comment_ids.bin")); err != nil {
		return nil, err
	}
	if p.Strings, err = mmapFile(filepath.Join(dir, "strings.bin")); err != nil {
		return nil, err
	}
	if expRaw := os.Getenv("ISUREAL_SIG_EXP"); expRaw != "" {
		exp, err := strconv.ParseInt(expRaw, 10, 64)
		if err != nil {
			return nil, err
		}
		suffix := "." + expRaw + ".bin"
		if p.PostSigs, err = mapSlice[[32]byte](filepath.Join(dir, "post_sigs"+suffix)); err != nil {
			return nil, err
		}
		if p.RMSigs, err = mapSlice[[32]byte](filepath.Join(dir, "rm_sigs"+suffix)); err != nil {
			return nil, err
		}
		if p.UserSigs, err = mapSlice[[32]byte](filepath.Join(dir, "user_sigs"+suffix)); err != nil {
			return nil, err
		}
		p.SigExp = exp
	} else {
		p.PostSigs = make([][32]byte, len(p.Posts))
		p.RMSigs = make([][32]byte, len(p.RMs))
		p.UserSigs = make([][32]byte, len(p.Init.Users))
	}
	sigExp.Store(p.SigExp)
	p.imageFile, err = os.Open(filepath.Join(dir, "images.dat"))
	if err != nil {
		return nil, err
	}
	st, err := p.imageFile.Stat()
	if err != nil {
		return nil, err
	}
	p.imageFileSz = uint64(st.Size())
	return p, nil
}

func (p *Pack) str(off uint32, n uint16) string {
	if n == 0 {
		return ""
	}
	return string(p.Strings[off : off+uint32(n)])
}

func findIndex(idx []IDIndex, id ID) (uint32, bool) {
	i := sort.Search(len(idx), func(i int) bool { return bytes.Compare(idx[i].ID[:], id[:]) >= 0 })
	if i < len(idx) && idx[i].ID == id {
		return idx[i].Idx, true
	}
	return 0, false
}

func (p *Pack) FindImage(id ID) *HistImage {
	imgs := p.Images
	lo, hi := 0, len(imgs)
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		c := bytes.Compare(imgs[m].ID[:], id[:])
		if c == 0 {
			return &imgs[m]
		}
		if c < 0 {
			lo = m + 1
		} else {
			hi = m
		}
	}
	return nil
}

// ReadImage は過去の画像の本体を buf へ読む。手元のファイルに無ければ false を返す。
func (p *Pack) ReadImage(e *HistImage, buf []byte) (bool, error) {
	if e.Off+uint64(e.Len) > p.imageFileSz {
		return false, nil
	}
	_, err := p.imageFile.ReadAt(buf[:e.Len], int64(e.Off))
	return err == nil, err
}
