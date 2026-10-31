// sigbuild は過去の画像の署名を、決めた期限について前もって計算して表にする。
package main

import (
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"example.com/isureal-company/isureal"
)

func main() {
	dir := os.Args[1]
	exp, err := strconv.ParseInt(os.Args[2], 10, 64)
	if err != nil {
		log.Fatal(err)
	}
	suffix := fmt.Sprintf("%d", exp)
	pack, err := isureal.OpenPack(dir)
	if err != nil {
		log.Fatal(err)
	}
	keys := make([][32]byte, len(pack.Init.Users))
	for i := range pack.Init.Users {
		b, _ := hex.DecodeString(pack.Init.Users[i].SecretKeyHash)
		copy(keys[i][:], b)
	}
	postSigs := make([][32]byte, len(pack.Posts))
	rmSigs := make([][32]byte, len(pack.RMs))
	userSigs := make([][32]byte, len(pack.Init.Users))
	type job struct {
		key *[32]byte
		id  isureal.ID
		out *[32]byte
	}
	jobs := make(chan job, 4096)
	var wg sync.WaitGroup
	var done atomic.Int64
	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				*j.out = isureal.FullImageSignature(j.key, j.id, exp)
				done.Add(1)
			}
		}()
	}
	start := time.Now()
	go func() {
		for range time.Tick(30 * time.Second) {
			log.Println("signed", done.Load(), time.Since(start))
		}
	}()
	for i := range pack.Init.Users {
		if id := pack.Init.Users[i].ProfileImageID; id != nil {
			pid, _ := isureal.ParseID(*id)
			jobs <- job{&keys[i], pid, &userSigs[i]}
		}
	}
	for i := range pack.Posts {
		p := &pack.Posts[i]
		if !p.DeletedInPack() {
			jobs <- job{&keys[p.Author], p.ImageID, &postSigs[i]}
		}
	}
	for i := range pack.RMs {
		r := &pack.RMs[i]
		if r.Replaced == 0 {
			jobs <- job{&keys[r.Reactor], r.ImageID, &rmSigs[i]}
		}
	}
	close(jobs)
	wg.Wait()
	write(filepath.Join(dir, "post_sigs."+suffix+".bin"), postSigs)
	write(filepath.Join(dir, "rm_sigs."+suffix+".bin"), rmSigs)
	write(filepath.Join(dir, "user_sigs."+suffix+".bin"), userSigs)
	log.Println("done", done.Load(), time.Since(start))
}

func write(path string, s [][32]byte) {
	b := unsafe.Slice((*byte)(unsafe.Pointer(&s[0])), 32*len(s))
	if err := os.WriteFile(path, b, 0o644); err != nil {
		log.Fatal(err)
	}
}
