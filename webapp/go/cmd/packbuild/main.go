// packbuild は初期データの DB から、過去データのファイル一式を作る（asia2 で1回だけ流す）。
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"
	"unsafe"

	"example.com/isureal-company/isureal"
	"github.com/jackc/pgx/v5"
)

type ID = isureal.ID

var out string

func main() {
	out = os.Args[1]
	dsn := os.Args[2]
	ctx := context.Background()
	if err := os.MkdirAll(out, 0o755); err != nil {
		log.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	must(err)
	defer conn.Close(ctx)
	var boundary time.Time
	must(conn.QueryRow(ctx, `SELECT created_at FROM initial_data_boundary`).Scan(&boundary))
	log.Println("boundary", boundary)

	init := &isureal.InitialData{}
	// users
	userIdx := map[ID]uint32{}
	regionOf := []string{}
	{
		rows, err := conn.Query(ctx, `SELECT id, username, secret_key_hash, display_name, bio, profile_image_id, region, account_type, status FROM users ORDER BY id`)
		must(err)
		for rows.Next() {
			var u isureal.InitUser
			must(rows.Scan(&u.ID, &u.Username, &u.SecretKeyHash, &u.DisplayName, &u.Bio, &u.ProfileImageID, &u.Region, &u.AccountType, &u.Status))
			id, _ := isureal.ParseID(u.ID)
			userIdx[id] = uint32(len(init.Users))
			regionOf = append(regionOf, u.Region)
			init.Users = append(init.Users, u)
		}
		must(rows.Err())
	}
	log.Println("users", len(init.Users))
	// windows
	windowIdx := map[string]uint32{}
	type wInfo struct {
		date     int32
		deadline time.Time
	}
	{
		rows, err := conn.Query(ctx, `SELECT id, region, local_date, starts_at, on_time_deadline, ended_at FROM posting_windows ORDER BY region, local_date`)
		must(err)
		for rows.Next() {
			var w isureal.InitWindow
			var d time.Time
			must(rows.Scan(&w.ID, &w.Region, &d, &w.StartsAt, &w.OnTimeDeadline, &w.EndedAt))
			w.LocalDate = int32(d.Unix() / 86400)
			w.StartsAt = w.StartsAt.UTC()
			w.OnTimeDeadline = w.OnTimeDeadline.UTC()
			if w.EndedAt != nil {
				t := w.EndedAt.UTC()
				w.EndedAt = &t
			}
			windowIdx[w.ID] = uint32(len(init.Windows))
			init.Windows = append(init.Windows, w)
		}
		must(rows.Err())
	}
	// relations
	{
		rows, err := conn.Query(ctx, `SELECT id, user_low_id, user_high_id, updated_at FROM friendships`)
		must(err)
		for rows.Next() {
			var f isureal.InitFriendship
			must(rows.Scan(&f.ID, &f.Low, &f.High, &f.UpdatedAt))
			f.UpdatedAt = f.UpdatedAt.UTC()
			init.Friendships = append(init.Friendships, f)
		}
		must(rows.Err())
		rows, err = conn.Query(ctx, `SELECT id, follower_id, followee_id, updated_at FROM follows`)
		must(err)
		for rows.Next() {
			var f isureal.InitFollow
			must(rows.Scan(&f.ID, &f.Follower, &f.Followee, &f.UpdatedAt))
			f.UpdatedAt = f.UpdatedAt.UTC()
			init.Follows = append(init.Follows, f)
		}
		must(rows.Err())
		rows, err = conn.Query(ctx, `SELECT id, blocker_id, blocked_id FROM block_relations`)
		must(err)
		for rows.Next() {
			var b isureal.InitBlock
			must(rows.Scan(&b.ID, &b.Blocker, &b.Blocked))
			init.Blocks = append(init.Blocks, b)
		}
		must(rows.Err())
		rows, err = conn.Query(ctx, `SELECT id, requester_id, recipient_id, status, updated_at FROM friend_requests`)
		must(err)
		for rows.Next() {
			var f isureal.InitFriendRequest
			must(rows.Scan(&f.ID, &f.Requester, &f.Recipient, &f.Status, &f.UpdatedAt))
			f.UpdatedAt = f.UpdatedAt.UTC()
			init.FriendRequests = append(init.FriendRequests, f)
		}
		must(rows.Err())
		rows, err = conn.Query(ctx, `SELECT id, username, password_hash FROM administrators`)
		must(err)
		for rows.Next() {
			var a isureal.InitAdmin
			must(rows.Scan(&a.ID, &a.Username, &a.PasswordHash))
			init.Admins = append(init.Admins, a)
		}
		must(rows.Err())
	}
	log.Println("relations done")

	var stringsBuf bytes.Buffer
	addString := func(s string) (uint32, uint16) {
		off := uint32(stringsBuf.Len())
		stringsBuf.WriteString(s)
		return off, uint16(len(s))
	}

	// posts
	posts := make([]isureal.HistPost, 0, 700000)
	{
		rows, err := conn.Query(ctx, `SELECT id, author_id, posting_window_id, image_id, caption, visibility, location_kind, latitude::float8, longitude::float8, locality, posted_at, deleted_at FROM posts`)
		must(err)
		for rows.Next() {
			var id, author, window, image, caption, vis, kind string
			var lat, lon *float64
			var locality *string
			var postedAt time.Time
			var deletedAt *time.Time
			must(rows.Scan(&id, &author, &window, &image, &caption, &vis, &kind, &lat, &lon, &locality, &postedAt, &deletedAt))
			var p isureal.HistPost
			p.ID = mustParse(id)
			p.ImageID = mustParse(image)
			p.PostedAt = postedAt.UnixMicro()
			p.Author = userIdx[mustParse(author)]
			p.Window = windowIdx[window]
			p.LocalDate = init.Windows[p.Window].LocalDate
			p.CaptionOff, p.CaptionLen = addString(caption)
			switch vis {
			case "friends":
				p.Flags = isureal.PostVisFriends
			case "friends_of_friends":
				p.Flags = isureal.PostVisFoF
			case "public":
				p.Flags = isureal.PostVisPublic
			default:
				log.Fatal("vis", vis)
			}
			switch kind {
			case "none":
				p.Flags |= isureal.LocNone << 4
			case "exact":
				p.Flags |= isureal.LocExact << 4
				if lat != nil {
					p.Lat = *lat
				}
				if lon != nil {
					p.Lon = *lon
				}
			case "approximate":
				p.Flags |= isureal.LocApproximate << 4
				if locality != nil {
					p.LocalityOff, p.LocalityLen = addString(*locality)
				}
			default:
				log.Fatal("kind", kind)
			}
			if deletedAt != nil && !deletedAt.After(boundary) {
				p.Flags |= 1 << 8
			}
			posts = append(posts, p)
		}
		must(rows.Err())
	}
	sort.Slice(posts, func(i, j int) bool {
		a, b := &posts[i], &posts[j]
		if a.Author != b.Author {
			return a.Author < b.Author
		}
		if a.PostedAt != b.PostedAt {
			return a.PostedAt > b.PostedAt
		}
		return bytes.Compare(a.ID[:], b.ID[:]) > 0
	})
	postIdx := make(map[ID]uint32, len(posts))
	for i := range posts {
		postIdx[posts[i].ID] = uint32(i)
		a := posts[i].Author
		if init.Users[a].PostEnd == 0 {
			init.Users[a].PostStart = uint32(i)
		}
		init.Users[a].PostEnd = uint32(i + 1)
	}
	init.NumPosts = len(posts)
	log.Println("posts", len(posts))

	// realmojis
	rms := make([]isureal.HistRM, 0, 1300000)
	{
		rows, err := conn.Query(ctx, `SELECT id, post_id, reactor_id, image_id, reaction_type, created_at, replaced_at, deleted_at FROM realmojis`)
		must(err)
		for rows.Next() {
			var id, post, reactor, image, typ string
			var createdAt time.Time
			var replacedAt, deletedAt *time.Time
			must(rows.Scan(&id, &post, &reactor, &image, &typ, &createdAt, &replacedAt, &deletedAt))
			var r isureal.HistRM
			r.ID = mustParse(id)
			r.ImageID = mustParse(image)
			r.CreatedAt = createdAt.UnixMicro()
			r.Post = postIdx[mustParse(post)]
			r.Reactor = userIdx[mustParse(reactor)]
			r.Type = isureal.ReactionTypeIndex(typ)
			if (replacedAt != nil && !replacedAt.After(boundary)) || (deletedAt != nil && !deletedAt.After(boundary)) {
				r.Replaced = 1
			}
			rms = append(rms, r)
		}
		must(rows.Err())
	}
	sort.Slice(rms, func(i, j int) bool {
		a, b := &rms[i], &rms[j]
		if a.Post != b.Post {
			return a.Post < b.Post
		}
		if a.CreatedAt != b.CreatedAt {
			return a.CreatedAt > b.CreatedAt
		}
		return bytes.Compare(a.ID[:], b.ID[:]) > 0
	})
	for i := 0; i < len(rms); {
		j := i
		for j < len(rms) && rms[j].Post == rms[i].Post {
			j++
		}
		posts[rms[i].Post].RMStart, posts[rms[i].Post].RMEnd = uint32(i), uint32(j)
		i = j
	}
	log.Println("realmojis", len(rms))

	// comments
	comments := make([]isureal.HistComment, 0, 600000)
	{
		rows, err := conn.Query(ctx, `SELECT id, post_id, author_id, body, created_at, deleted_at FROM comments`)
		must(err)
		for rows.Next() {
			var id, post, author, body string
			var createdAt time.Time
			var deletedAt *time.Time
			must(rows.Scan(&id, &post, &author, &body, &createdAt, &deletedAt))
			var c isureal.HistComment
			c.ID = mustParse(id)
			c.CreatedAt = createdAt.UnixMicro()
			c.Post = postIdx[mustParse(post)]
			c.Author = userIdx[mustParse(author)]
			c.BodyOff, c.BodyLen = addString(body)
			if deletedAt != nil && !deletedAt.After(boundary) {
				c.Deleted = 1
			}
			comments = append(comments, c)
		}
		must(rows.Err())
	}
	sort.Slice(comments, func(i, j int) bool {
		a, b := &comments[i], &comments[j]
		if a.Post != b.Post {
			return a.Post < b.Post
		}
		if a.CreatedAt != b.CreatedAt {
			return a.CreatedAt < b.CreatedAt
		}
		return bytes.Compare(a.ID[:], b.ID[:]) < 0
	})
	for i := 0; i < len(comments); {
		j := i
		for j < len(comments) && comments[j].Post == comments[i].Post {
			j++
		}
		posts[comments[i].Post].CStart, posts[comments[i].Post].CEnd = uint32(i), uint32(j)
		i = j
	}
	log.Println("comments", len(comments))

	// 画像の並びを決める: プロフィール → europe/america の投稿 → asia の投稿。
	type imgInfo struct {
		id     ID
		owner  uint32
		ref    uint32
		kind   uint8
		length uint32
		off    uint64
		crc    uint32
		valid  bool
	}
	imgs := make(map[ID]*imgInfo, 1900000)
	{
		rows, err := conn.Query(ctx, `SELECT id, owner_user_id, octet_length(data) FROM image_objects WHERE created_at <= $1`, boundary)
		must(err)
		for rows.Next() {
			var id, owner string
			var n int32
			must(rows.Scan(&id, &owner, &n))
			pid := mustParse(id)
			imgs[pid] = &imgInfo{id: pid, owner: userIdx[mustParse(owner)], length: uint32(n)}
		}
		must(rows.Err())
	}
	log.Println("images", len(imgs))
	var offset uint64
	place := func(id ID, kind uint8, ref uint32, valid bool) {
		in := imgs[id]
		if in == nil {
			log.Println("missing image", id.String(), kind)
			return
		}
		in.kind, in.ref, in.valid = kind, ref, valid
		if valid {
			in.off = offset
			offset += uint64(in.length)
		}
	}
	for i := range init.Users {
		u := &init.Users[i]
		if u.ProfileImageID != nil {
			place(mustParse(*u.ProfileImageID), isureal.ImageKindProfile, uint32(i), u.Status == "active")
		}
	}
	placePost := func(i int) {
		p := &posts[i]
		place(p.ImageID, isureal.ImageKindPost, uint32(i), !p.DeletedInPack())
		for r := p.RMStart; r < p.RMEnd; r++ {
			place(rms[r].ImageID, isureal.ImageKindRealmoji, r, rms[r].Replaced == 0)
		}
	}
	for i := range posts {
		if regionOf[posts[i].Author] != "asia" {
			placePost(i)
		}
	}
	init.PrefixLen = offset
	log.Println("prefix", offset)
	for i := range posts {
		if regionOf[posts[i].Author] == "asia" {
			placePost(i)
		}
	}
	log.Println("total image bytes", offset)

	// 画像の本体を DB の並びのまま読み、決めた位置へ書く
	f, err := os.Create(filepath.Join(out, "images.dat"))
	must(err)
	must(f.Truncate(int64(offset)))
	{
		rows, err := conn.Query(ctx, `SELECT id, data FROM image_objects WHERE created_at <= $1`, boundary)
		must(err)
		n := 0
		start := time.Now()
		for rows.Next() {
			var id string
			var data []byte
			must(rows.Scan(&id, &data))
			in := imgs[mustParse(id)]
			if in == nil {
				continue
			}
			in.crc = crc32.ChecksumIEEE(data)
			if in.valid {
				_, err := f.WriteAt(data, int64(in.off))
				must(err)
			}
			n++
			if n%100000 == 0 {
				log.Println("images written", n, time.Since(start))
			}
		}
		must(rows.Err())
	}
	must(f.Sync())
	must(f.Close())

	imgList := make([]isureal.HistImage, 0, len(imgs))
	for _, in := range imgs {
		e := isureal.HistImage{ID: in.id, Off: in.off, CRC: in.crc, Owner: in.owner, Ref: in.ref, Kind: in.kind}
		if in.valid {
			e.Len = in.length
		}
		imgList = append(imgList, e)
	}
	sort.Slice(imgList, func(i, j int) bool { return bytes.Compare(imgList[i].ID[:], imgList[j].ID[:]) < 0 })

	writeSlice("posts.bin", posts)
	writeSlice("realmojis.bin", rms)
	writeSlice("comments.bin", comments)
	writeSlice("images.idx", imgList)
	writeSlice("post_ids.bin", buildIndex(len(posts), func(i int) ID { return posts[i].ID }))
	writeSlice("realmoji_ids.bin", buildIndex(len(rms), func(i int) ID { return rms[i].ID }))
	writeSlice("comment_ids.bin", buildIndex(len(comments), func(i int) ID { return comments[i].ID }))
	must(os.WriteFile(filepath.Join(out, "strings.bin"), stringsBuf.Bytes(), 0o644))
	bw, err := os.Create(filepath.Join(out, "initial.json"))
	must(err)
	w := bufio.NewWriter(bw)
	must(json.NewEncoder(w).Encode(init))
	must(w.Flush())
	must(bw.Close())
	fmt.Println("done", binary.Size(isureal.HistImage{}), unsafe.Sizeof(isureal.HistPost{}), unsafe.Sizeof(isureal.HistRM{}), unsafe.Sizeof(isureal.HistComment{}))
}

func buildIndex(n int, id func(int) ID) []isureal.IDIndex {
	idx := make([]isureal.IDIndex, n)
	for i := 0; i < n; i++ {
		idx[i] = isureal.IDIndex{ID: id(i), Idx: uint32(i)}
	}
	sort.Slice(idx, func(i, j int) bool { return bytes.Compare(idx[i].ID[:], idx[j].ID[:]) < 0 })
	return idx
}

func writeSlice[T any](name string, s []T) {
	f, err := os.Create(filepath.Join(out, name))
	must(err)
	if len(s) > 0 {
		var zero T
		size := int(unsafe.Sizeof(zero))
		b := unsafe.Slice((*byte)(unsafe.Pointer(&s[0])), size*len(s))
		_, err = f.Write(b)
		must(err)
	}
	must(f.Close())
	log.Println("wrote", name, len(s))
}

func mustParse(s string) ID {
	id, ok := isureal.ParseID(s)
	if !ok {
		log.Fatal("bad id ", s)
	}
	return id
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
