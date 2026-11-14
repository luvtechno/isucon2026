-- 競技で足したインデックス（初期データに対して1回だけ流す。何度流してもよい）
CREATE INDEX IF NOT EXISTS login_sessions_token_hash ON login_sessions(token_hash) WHERE active;
CREATE INDEX IF NOT EXISTS login_sessions_user_id ON login_sessions(user_id) WHERE active;
CREATE INDEX IF NOT EXISTS friendships_user_high_id ON friendships(user_high_id);
CREATE INDEX IF NOT EXISTS follows_followee_id ON follows(followee_id);
CREATE INDEX IF NOT EXISTS block_relations_blocked_id ON block_relations(blocked_id);
CREATE INDEX IF NOT EXISTS users_username ON users(username);
CREATE INDEX IF NOT EXISTS friend_requests_recipient ON friend_requests(recipient_id) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS friend_requests_requester ON friend_requests(requester_id) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS posts_image_id ON posts(image_id);
CREATE INDEX IF NOT EXISTS realmojis_image_id ON realmojis(image_id);
CREATE INDEX IF NOT EXISTS posting_windows_local_date ON posting_windows(local_date);
