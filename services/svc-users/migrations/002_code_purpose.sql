-- specs/021：验证码复用 reset_codes 表，增加用途区分（reset=找回密码，verify=邮箱验证）。
ALTER TABLE reset_codes ADD COLUMN purpose TEXT NOT NULL DEFAULT 'reset';
CREATE INDEX reset_codes_purpose_idx ON reset_codes (user_id, purpose);
