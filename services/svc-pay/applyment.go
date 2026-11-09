// 进件与审核（specs/030 M2）。
// 操作员录入归一资料（图片经 svc-file 上传后在此拉取留存）→ 提交审核 → 审核员通过/驳回
// → 通过后提交渠道（图片先传渠道换 media_id）→ 轮询渠道状态直到 finished，回填 sub_mchid。
// 所有状态流转 CAS；驳回可改资料重提，business_code 沿用。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"platfarm/svc-pay/channel"
)

// ApplymentRow pay_applyment 一行。Payload 对外输出前已脱敏。
type ApplymentRow struct {
	ApplymentID        string          `json:"applymentId"`
	IsvNo              string          `json:"isvNo"`
	MchNo              string          `json:"mchNo"`
	IfCode             string          `json:"ifCode"`
	BusinessCode       string          `json:"businessCode"`
	Payload            json.RawMessage `json:"payload"`
	State              string          `json:"state"`
	RejectReason       string          `json:"rejectReason"`
	Reviewer           string          `json:"reviewer"`
	ReviewedAt         *time.Time      `json:"reviewedAt"`
	ChannelApplymentID string          `json:"channelApplymentId"`
	ChannelState       string          `json:"channelState"`
	ChannelMsg         string          `json:"channelMsg"`
	SignURL            string          `json:"signUrl"`
	SubMchID           string          `json:"subMchid"`
	Unexpressed        string          `json:"unexpressed"`
	PollCount          int             `json:"pollCount"`
	NextPollAt         *time.Time      `json:"nextPollAt"`
	CreatedBy          string          `json:"createdBy"`
	TenantID           int             `json:"tenantId"`
	CreatedAt          time.Time       `json:"createdAt"`
	UpdatedAt          time.Time       `json:"updatedAt"`
}

const applymentCols = `applyment_id, isv_no, mch_no, if_code, business_code, payload, state, reject_reason, reviewer,
	reviewed_at, channel_applyment_id, channel_state, channel_msg, sign_url, sub_mchid, unexpressed, poll_count,
	next_poll_at, created_by, tenant_id, created_at, updated_at`

func scanApplyment(row pgx.Row) (ApplymentRow, error) {
	var a ApplymentRow
	err := row.Scan(&a.ApplymentID, &a.IsvNo, &a.MchNo, &a.IfCode, &a.BusinessCode, &a.Payload, &a.State,
		&a.RejectReason, &a.Reviewer, &a.ReviewedAt, &a.ChannelApplymentID, &a.ChannelState, &a.ChannelMsg,
		&a.SignURL, &a.SubMchID, &a.Unexpressed, &a.PollCount, &a.NextPollAt, &a.CreatedBy, &a.TenantID,
		&a.CreatedAt, &a.UpdatedAt)
	return a, err
}

func findApplyment(ctx context.Context, id string) (ApplymentRow, error) {
	return scanApplyment(db.QueryRow(ctx, `SELECT `+applymentCols+` FROM pay_applyment WHERE applyment_id=$1`, id))
}

// 轮询的状态集合：已交渠道、渠道未出终态。
var pollingStates = []string{channel.ApplymentSubmitted, channel.ApplymentChannelAuditing,
	channel.ApplymentNeedSign, channel.ApplymentNeedVerify}

// ── 密封 / 脱敏 ──────────────────────────────────────────────

// sealPayload 落库前密封证件号与银行账号。
func sealPayload(a channel.Applyment) (channel.Applyment, error) {
	var err error
	if a.LegalPerson.IDNo, err = seal(a.LegalPerson.IDNo); err != nil {
		return a, err
	}
	if a.SettlementAccount.AccountNo, err = seal(a.SettlementAccount.AccountNo); err != nil {
		return a, err
	}
	return a, nil
}

// openPayload 解封（提交渠道前）。
func openPayload(a channel.Applyment) (channel.Applyment, error) {
	var err error
	if a.LegalPerson.IDNo, err = open(a.LegalPerson.IDNo); err != nil {
		return a, err
	}
	if a.SettlementAccount.AccountNo, err = open(a.SettlementAccount.AccountNo); err != nil {
		return a, err
	}
	return a, nil
}

// maskedPayload 对外输出：证件号与账号脱敏，解密失败则整体打码。
func maskedPayload(raw json.RawMessage) json.RawMessage {
	var a channel.Applyment
	if json.Unmarshal(raw, &a) != nil {
		return raw
	}
	if plain, err := open(a.LegalPerson.IDNo); err == nil {
		a.LegalPerson.IDNo = channel.MaskID(plain)
	} else {
		a.LegalPerson.IDNo = "******"
	}
	if plain, err := open(a.SettlementAccount.AccountNo); err == nil {
		a.SettlementAccount.AccountNo = channel.MaskID(plain)
	} else {
		a.SettlementAccount.AccountNo = "******"
	}
	out, _ := json.Marshal(a)
	return out
}

func (a ApplymentRow) masked() ApplymentRow {
	a.Payload = maskedPayload(a.Payload)
	return a
}

// ── 图片留存 ─────────────────────────────────────────────────

type mediaRow struct {
	Kind     string `json:"kind"`
	FileID   string `json:"fileId"`
	Filename string `json:"filename"`
	Mime     string `json:"mime"`
	Size     int    `json:"size"`
	MediaID  string `json:"mediaId"`
	Content  []byte `json:"-"`
}

// storeMedia 按 images 拉取每张图并留存；不再引用的 kind 删除。同一 file_id 不重复拉取。
func storeMedia(ctx context.Context, bearer, applymentID string, images map[string]string) error {
	if len(images) > 12 {
		return fmt.Errorf("images 最多 12 张")
	}
	existing := map[string]string{} // kind → file_id
	rows, err := db.Query(ctx, `SELECT kind, file_id FROM pay_applyment_media WHERE applyment_id=$1`, applymentID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var k, f string
		if rows.Scan(&k, &f) == nil {
			existing[k] = f
		}
	}
	rows.Close()
	for kind := range existing {
		if _, still := images[kind]; !still {
			if _, err := db.Exec(ctx, `DELETE FROM pay_applyment_media WHERE applyment_id=$1 AND kind=$2`, applymentID, kind); err != nil {
				return err
			}
		}
	}
	for kind, fileID := range images {
		if existing[kind] == fileID {
			continue
		}
		f, err := fetchFile(ctx, bearer, fileID)
		if err != nil {
			return fmt.Errorf("images.%s: %w", kind, err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO pay_applyment_media
			(applyment_id, kind, file_id, filename, mime, size, sha256, content)
			VALUES ($1,$2,$3,$4,$5,$6,'',$7)
			ON CONFLICT (applyment_id, kind) DO UPDATE SET file_id=$3, filename=$4, mime=$5, size=$6,
				content=$7, media_id='', uploaded_at=NULL`,
			applymentID, kind, fileID, f.Filename, f.Mime, f.Size, f.Content); err != nil {
			return err
		}
	}
	return nil
}

func listMedia(ctx context.Context, applymentID string, withContent bool) ([]mediaRow, error) {
	cols := `kind, file_id, filename, mime, size, media_id`
	if withContent {
		cols += `, content`
	}
	rows, err := db.Query(ctx, `SELECT `+cols+` FROM pay_applyment_media WHERE applyment_id=$1 ORDER BY kind`, applymentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []mediaRow
	for rows.Next() {
		var m mediaRow
		var serr error
		if withContent {
			serr = rows.Scan(&m.Kind, &m.FileID, &m.Filename, &m.Mime, &m.Size, &m.MediaID, &m.Content)
		} else {
			serr = rows.Scan(&m.Kind, &m.FileID, &m.Filename, &m.Mime, &m.Size, &m.MediaID)
		}
		if serr == nil {
			out = append(out, m)
		}
	}
	return out, rows.Err()
}

// ── 状态流转（CAS） ──────────────────────────────────────────

func casApplyment(ctx context.Context, id string, from []string, to string) (bool, error) {
	tag, err := db.Exec(ctx, `UPDATE pay_applyment SET state=$3, updated_at=now()
		WHERE applyment_id=$1 AND state = ANY($2)`, id, from, to)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func reviewApplyment(ctx context.Context, id, to, reviewer, reason string) (bool, error) {
	tag, err := db.Exec(ctx, `UPDATE pay_applyment SET state=$2, reviewer=$3, reject_reason=$4,
		reviewed_at=now(), updated_at=now() WHERE applyment_id=$1 AND state='reviewing'`, id, to, reviewer, reason)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func logTransition(id, from, to, note string) {
	log.Printf(`{"event":"applyment","applymentId":%q,"from":%q,"to":%q,"note":%q}`, id, from, to, note)
}

// submitToChannel approved → submitted：上传缺 media_id 的图片，提交申请单。
// 渠道报错时保持 approved 并记录 channel_msg，可重试。
func submitToChannel(ctx context.Context, a ApplymentRow) (ApplymentRow, error) {
	ch, err := channel.Onboarding(a.IfCode)
	if err != nil {
		return a, err
	}
	provider, err := loadChannelParams(ctx, "isv", a.IsvNo, a.IfCode)
	if err != nil {
		return a, fmt.Errorf("服务商 %s 渠道参数未配置", a.IsvNo)
	}
	var payload channel.Applyment
	if err := json.Unmarshal(a.Payload, &payload); err != nil {
		return a, err
	}
	payload, err = openPayload(payload)
	if err != nil {
		return a, fmt.Errorf("解封资料失败: %w", err)
	}
	media, err := listMedia(ctx, a.ApplymentID, true)
	if err != nil {
		return a, err
	}
	mediaIDs := map[string]string{}
	for _, m := range media {
		if m.MediaID == "" {
			id, uerr := ch.UploadMedia(ctx, m.Filename, m.Content, provider)
			if uerr != nil {
				_ = noteSubmitError(ctx, a.ApplymentID, "上传图片 "+m.Kind+": "+uerr.Error())
				return a, uerr
			}
			if _, err := db.Exec(ctx, `UPDATE pay_applyment_media SET media_id=$3, uploaded_at=now()
				WHERE applyment_id=$1 AND kind=$2`, a.ApplymentID, m.Kind, id); err != nil {
				return a, err
			}
			m.MediaID = id
		}
		mediaIDs[m.Kind] = m.MediaID
	}
	res, err := ch.SubmitApplyment(ctx, channel.ApplymentIn{
		BusinessCode: a.BusinessCode, Payload: payload, Media: mediaIDs, Provider: provider,
	})
	if err != nil {
		_ = noteSubmitError(ctx, a.ApplymentID, err.Error())
		return a, err
	}
	tag, err := db.Exec(ctx, `UPDATE pay_applyment SET state='submitted', channel_applyment_id=$2, unexpressed=$3,
		channel_msg='', poll_count=0, next_poll_at=now()+interval '1 minute', updated_at=now()
		WHERE applyment_id=$1 AND state='approved'`, a.ApplymentID, res.ChannelApplymentID, strings.Join(res.Unexpressed, ","))
	if err != nil {
		return a, err
	}
	if tag.RowsAffected() == 1 {
		logTransition(a.ApplymentID, channel.ApplymentApproved, channel.ApplymentSubmitted, res.ChannelApplymentID)
	}
	return findApplyment(ctx, a.ApplymentID)
}

func noteSubmitError(ctx context.Context, id, msg string) error {
	_, err := db.Exec(ctx, `UPDATE pay_applyment SET channel_msg=$2, updated_at=now() WHERE applyment_id=$1`, id, trunc(msg, 500))
	return err
}

// syncFromChannel 查一次渠道并按返回推进状态。渠道未知状态只更新轮询计划，不动 state。
func syncFromChannel(ctx context.Context, a ApplymentRow) (ApplymentRow, error) {
	ch, err := channel.Onboarding(a.IfCode)
	if err != nil {
		return a, err
	}
	provider, err := loadChannelParams(ctx, "isv", a.IsvNo, a.IfCode)
	if err != nil {
		return a, fmt.Errorf("服务商 %s 渠道参数未配置", a.IsvNo)
	}
	view, err := ch.QueryApplyment(ctx, a.BusinessCode, provider)
	if err != nil {
		_, _ = db.Exec(ctx, `UPDATE pay_applyment SET channel_msg=$2, poll_count=poll_count+1,
			next_poll_at=now()+$3::interval, updated_at=now() WHERE applyment_id=$1`,
			a.ApplymentID, trunc(err.Error(), 500), pollBackoff(a.PollCount+1).String())
		return a, err
	}
	return applyChannelView(ctx, a, view)
}

func applyChannelView(ctx context.Context, a ApplymentRow, view channel.ApplymentView) (ApplymentRow, error) {
	to := view.State
	if to == "" || to == a.State {
		// 未变化：只刷新渠道字段与下一次轮询时间。
		_, err := db.Exec(ctx, `UPDATE pay_applyment SET channel_state=$2, channel_msg=$3, sign_url=$4,
			channel_applyment_id=COALESCE(NULLIF($5,''), channel_applyment_id), poll_count=poll_count+1,
			next_poll_at=now()+$6::interval, updated_at=now() WHERE applyment_id=$1`,
			a.ApplymentID, view.RawState, trunc(view.Message, 500), view.SignURL, view.ChannelApplymentID,
			pollBackoff(a.PollCount+1).String())
		if err != nil {
			return a, err
		}
		return findApplyment(ctx, a.ApplymentID)
	}
	nextPoll := any(nil)
	if !channel.IsTerminalApplyment(to) && to != channel.ApplymentRejected {
		nextPoll = time.Now().Add(pollBackoff(0))
	}
	tag, err := db.Exec(ctx, `UPDATE pay_applyment SET state=$3, channel_state=$4, channel_msg=$5, sign_url=$6,
		sub_mchid=COALESCE(NULLIF($7,''), sub_mchid), channel_applyment_id=COALESCE(NULLIF($8,''), channel_applyment_id),
		reject_reason=CASE WHEN $3='rejected' THEN $5 ELSE reject_reason END,
		poll_count=0, next_poll_at=$9, updated_at=now()
		WHERE applyment_id=$1 AND state=$2`,
		a.ApplymentID, a.State, to, view.RawState, trunc(view.Message, 500), view.SignURL, view.SubMchID,
		view.ChannelApplymentID, nextPoll)
	if err != nil {
		return a, err
	}
	if tag.RowsAffected() == 1 {
		logTransition(a.ApplymentID, a.State, to, view.RawState)
		if to == channel.ApplymentFinished && view.SubMchID != "" {
			if err := onApplymentFinished(ctx, a, view.SubMchID); err != nil {
				log.Printf("applyment %s finished but sub_mchid write-back failed: %v", a.ApplymentID, err)
			}
		}
	}
	return findApplyment(ctx, a.ApplymentID)
}

// pollBackoff 1m, 2m, 4m … 封顶 1h。
func pollBackoff(n int) time.Duration {
	d := time.Minute
	for i := 0; i < n && d < time.Hour; i++ {
		d *= 2
	}
	if d > time.Hour {
		d = time.Hour
	}
	return d
}

// onApplymentFinished 把 sub_mchid 并进商户的渠道参数（保留已有键），并确保商户挂到该服务商。
func onApplymentFinished(ctx context.Context, a ApplymentRow, subMchID string) error {
	params, err := loadChannelParams(ctx, "merchant", a.MchNo, a.IfCode)
	if err != nil {
		if !notFound(err) {
			return err
		}
		params = map[string]string{}
	}
	params["sub_mchid"] = subMchID
	sealed, err := mustSealParams(params)
	if err != nil {
		return err
	}
	if _, err := db.Exec(ctx, `INSERT INTO pay_channel_config (owner_type, owner_id, if_code, if_params, tenant_id)
		VALUES ('merchant',$1,$2,$3,$4)
		ON CONFLICT (owner_type, owner_id, if_code) DO UPDATE SET if_params=$3, state='active', updated_at=now()`,
		a.MchNo, a.IfCode, sealed, a.TenantID); err != nil {
		return err
	}
	_, err = db.Exec(ctx, `UPDATE pay_merchant SET isv_no=CASE WHEN isv_no='' THEN $2 ELSE isv_no END,
		state='active', updated_at=now() WHERE mch_no=$1`, a.MchNo, a.IsvNo)
	return err
}

// applymentWorker 每分钟轮询渠道未出终态的申请单（渠道不一定推送）。
func applymentWorker(ctx context.Context) {
	everyLeased(ctx, "applyments", 60*time.Second, pollApplyments)
}

func pollApplyments(ctx context.Context) error {
	rows, err := db.Query(ctx, `SELECT `+applymentCols+` FROM pay_applyment
		WHERE state = ANY($1) AND (next_poll_at IS NULL OR next_poll_at <= now())
		ORDER BY next_poll_at NULLS FIRST LIMIT 20`, pollingStates)
	if err != nil {
		return err
	}
	var batch []ApplymentRow
	for rows.Next() {
		if a, serr := scanApplyment(rows); serr == nil {
			batch = append(batch, a)
		}
	}
	rows.Close()
	for _, a := range batch {
		if _, err := syncFromChannel(ctx, a); err != nil {
			log.Printf("applyment %s sync: %v", a.ApplymentID, err)
		}
	}
	return nil
}

// ── 管理接口 ─────────────────────────────────────────────────

type applymentIn struct {
	MchNo   string            `json:"mchNo"`
	IfCode  string            `json:"ifCode"`
	Payload channel.Applyment `json:"payload"`
}

// handleCreateApplyment POST /admin/applyments：operator 建草稿。
func handleCreateApplyment(c *gin.Context) {
	claims, ok := requireRole(c, roleOperator)
	if !ok {
		return
	}
	var in applymentIn
	if c.ShouldBindJSON(&in) != nil || in.MchNo == "" || in.IfCode == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mchNo、ifCode、payload 必填"})
		return
	}
	ctx := c.Request.Context()
	if _, err := channel.Onboarding(in.IfCode); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	mch, err := loadMerchant(ctx, in.MchNo)
	if notFound(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "商户不存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询商户失败"})
		return
	}
	if mch.IsvNo == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "商户未挂服务商（isvNo 为空），无法进件"})
		return
	}
	if denyOtherTenant(c, claims, mch.TenantID, "商户") {
		return
	}
	if problems := in.Payload.Problems(); len(problems) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "资料不完整", "problems": problems})
		return
	}
	sealed, err := sealPayload(in.Payload)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "密封资料失败"})
		return
	}
	raw, _ := json.Marshal(sealed)
	id := newOrderID("AP")
	if _, err := db.Exec(ctx, `INSERT INTO pay_applyment
		(applyment_id, isv_no, mch_no, if_code, business_code, payload, created_by, tenant_id)
		VALUES ($1,$2,$3,$4,$1,$5,$6,$7)`, id, mch.IsvNo, mch.MchNo, in.IfCode, raw, claims.Username, mch.TenantID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败"})
		return
	}
	if err := storeMedia(ctx, c.GetHeader("Authorization"), id, in.Payload.Images); err != nil {
		_, _ = db.Exec(ctx, `DELETE FROM pay_applyment WHERE applyment_id=$1`, id)
		c.JSON(http.StatusBadGateway, gin.H{"error": "拉取图片失败: " + err.Error()})
		return
	}
	logTransition(id, "", channel.ApplymentDraft, claims.Username)
	a, _ := findApplyment(ctx, id)
	c.JSON(http.StatusOK, gin.H{"applyment": a.masked()})
}

// handleUpdateApplyment PUT /admin/applyments/:id：draft|rejected 改资料 → draft。
func handleUpdateApplyment(c *gin.Context) {
	claims, ok := requireRole(c, roleOperator)
	if !ok {
		return
	}
	a, ok := loadApplymentFor(c, claims)
	if !ok {
		return
	}
	if a.State != channel.ApplymentDraft && a.State != channel.ApplymentRejected {
		c.JSON(http.StatusConflict, gin.H{"error": "状态 " + a.State + " 不可修改资料"})
		return
	}
	var in struct {
		Payload channel.Applyment `json:"payload"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "payload 必填"})
		return
	}
	if problems := in.Payload.Problems(); len(problems) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "资料不完整", "problems": problems})
		return
	}
	sealed, err := sealPayload(in.Payload)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "密封资料失败"})
		return
	}
	raw, _ := json.Marshal(sealed)
	ctx := c.Request.Context()
	if err := storeMedia(ctx, c.GetHeader("Authorization"), a.ApplymentID, in.Payload.Images); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "拉取图片失败: " + err.Error()})
		return
	}
	tag, err := db.Exec(ctx, `UPDATE pay_applyment SET payload=$3, state='draft', reject_reason='', channel_msg='',
		updated_at=now() WHERE applyment_id=$1 AND state=$2`, a.ApplymentID, a.State, raw)
	if err != nil || tag.RowsAffected() == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "状态已变化，请刷新"})
		return
	}
	logTransition(a.ApplymentID, a.State, channel.ApplymentDraft, "payload updated by "+claims.Username)
	a, _ = findApplyment(ctx, a.ApplymentID)
	c.JSON(http.StatusOK, gin.H{"applyment": a.masked()})
}

// handleSubmitReview POST /admin/applyments/:id/submit-review：draft → reviewing。
func handleSubmitReview(c *gin.Context) {
	claims, ok := requireRole(c, roleOperator)
	if !ok {
		return
	}
	a, ok := loadApplymentFor(c, claims)
	if !ok {
		return
	}
	moved, err := casApplyment(c.Request.Context(), a.ApplymentID, []string{channel.ApplymentDraft}, channel.ApplymentReviewing)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新失败"})
		return
	}
	if !moved {
		c.JSON(http.StatusConflict, gin.H{"error": "只有草稿可提交审核（当前 " + a.State + "）"})
		return
	}
	logTransition(a.ApplymentID, channel.ApplymentDraft, channel.ApplymentReviewing, claims.Username)
	a, _ = findApplyment(c.Request.Context(), a.ApplymentID)
	c.JSON(http.StatusOK, gin.H{"applyment": a.masked()})
}

// handleApproveApplyment POST /admin/applyments/:id/approve：reviewer；通过后立刻尝试提交渠道。
func handleApproveApplyment(c *gin.Context) {
	claims, ok := requireRole(c, roleReviewer)
	if !ok {
		return
	}
	a, ok := loadApplymentFor(c, claims)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	moved, err := reviewApplyment(ctx, a.ApplymentID, channel.ApplymentApproved, claims.Username, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新失败"})
		return
	}
	if !moved {
		c.JSON(http.StatusConflict, gin.H{"error": "只有审核中的申请可通过（当前 " + a.State + "）"})
		return
	}
	logTransition(a.ApplymentID, channel.ApplymentReviewing, channel.ApplymentApproved, claims.Username)
	a, _ = findApplyment(ctx, a.ApplymentID)
	a, serr := submitToChannel(ctx, a)
	if serr != nil {
		a, _ = findApplyment(ctx, a.ApplymentID)
		c.JSON(http.StatusOK, gin.H{"applyment": a.masked(), "channelError": serr.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"applyment": a.masked()})
}

// handleRejectApplyment POST /admin/applyments/:id/reject {reason}：reviewer；reviewing → rejected。
func handleRejectApplyment(c *gin.Context) {
	claims, ok := requireRole(c, roleReviewer)
	if !ok {
		return
	}
	a, ok := loadApplymentFor(c, claims)
	if !ok {
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.Reason) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reason 必填"})
		return
	}
	moved, err := reviewApplyment(c.Request.Context(), a.ApplymentID, channel.ApplymentRejected, claims.Username, in.Reason)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新失败"})
		return
	}
	if !moved {
		c.JSON(http.StatusConflict, gin.H{"error": "只有审核中的申请可驳回（当前 " + a.State + "）"})
		return
	}
	logTransition(a.ApplymentID, channel.ApplymentReviewing, channel.ApplymentRejected, in.Reason)
	a, _ = findApplyment(c.Request.Context(), a.ApplymentID)
	c.JSON(http.StatusOK, gin.H{"applyment": a.masked()})
}

// handleSubmitApplyment POST /admin/applyments/:id/submit：approved 状态重试提交渠道。
func handleSubmitApplyment(c *gin.Context) {
	claims, ok := requireRole(c, roleOperator)
	if !ok {
		return
	}
	a, ok := loadApplymentFor(c, claims)
	if !ok {
		return
	}
	if a.State != channel.ApplymentApproved {
		c.JSON(http.StatusConflict, gin.H{"error": "只有已通过审核的申请可提交渠道（当前 " + a.State + "）"})
		return
	}
	a, err := submitToChannel(c.Request.Context(), a)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"applyment": a.masked()})
}

// handleSyncApplyment POST /admin/applyments/:id/sync：立即查一次渠道。
func handleSyncApplyment(c *gin.Context) {
	claims, ok := requireRole(c, roleOperator)
	if !ok {
		return
	}
	a, ok := loadApplymentFor(c, claims)
	if !ok {
		return
	}
	if a.ChannelApplymentID == "" && a.State != channel.ApplymentSubmitted {
		c.JSON(http.StatusConflict, gin.H{"error": "尚未提交渠道"})
		return
	}
	a, err := syncFromChannel(c.Request.Context(), a)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"applyment": a.masked()})
}

// handleCancelApplyment POST /admin/applyments/:id/cancel：提交渠道前可取消（渠道侧无撤回接口）。
func handleCancelApplyment(c *gin.Context) {
	claims, ok := requireRole(c, roleOperator)
	if !ok {
		return
	}
	a, ok := loadApplymentFor(c, claims)
	if !ok {
		return
	}
	moved, err := casApplyment(c.Request.Context(), a.ApplymentID,
		[]string{channel.ApplymentDraft, channel.ApplymentReviewing, channel.ApplymentApproved, channel.ApplymentRejected},
		channel.ApplymentCanceled)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新失败"})
		return
	}
	if !moved {
		c.JSON(http.StatusConflict, gin.H{"error": "状态 " + a.State + " 不可取消"})
		return
	}
	logTransition(a.ApplymentID, a.State, channel.ApplymentCanceled, claims.Username)
	a, _ = findApplyment(c.Request.Context(), a.ApplymentID)
	c.JSON(http.StatusOK, gin.H{"applyment": a.masked()})
}

// handleListApplyments GET /admin/applyments?state=&mchNo=
func handleListApplyments(c *gin.Context) {
	claims, ok := requireRole(c, roleOperator)
	if !ok {
		return
	}
	scope, args := tenantScope(claims)
	sql := `SELECT ` + applymentCols + ` FROM pay_applyment WHERE ` + scope
	if s := c.Query("state"); s != "" {
		args = append(args, s)
		sql += fmt.Sprintf(` AND state=$%d`, len(args))
	}
	if m := c.Query("mchNo"); m != "" {
		args = append(args, m)
		sql += fmt.Sprintf(` AND mch_no=$%d`, len(args))
	}
	sql += ` ORDER BY created_at DESC LIMIT 200`
	rows, err := db.Query(c.Request.Context(), sql, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	defer rows.Close()
	items := []ApplymentRow{}
	for rows.Next() {
		if a, serr := scanApplyment(rows); serr == nil {
			items = append(items, a.masked())
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// handleGetApplyment GET /admin/applyments/:id：详情 + 图片清单（不含字节）。
func handleGetApplyment(c *gin.Context) {
	claims, ok := requireRole(c, roleOperator)
	if !ok {
		return
	}
	a, ok := loadApplymentFor(c, claims)
	if !ok {
		return
	}
	media, _ := listMedia(c.Request.Context(), a.ApplymentID, false)
	if media == nil {
		media = []mediaRow{}
	}
	c.JSON(http.StatusOK, gin.H{"applyment": a.masked(), "media": media})
}

// loadApplymentFor 取路径参数指向的申请单并做租户隔离；失败已写响应。
func loadApplymentFor(c *gin.Context, claims *Claims) (ApplymentRow, bool) {
	a, err := findApplyment(c.Request.Context(), c.Param("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "申请单不存在"})
		return a, false
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return a, false
	}
	if denyOtherTenant(c, claims, a.TenantID, "申请单") {
		return a, false
	}
	return a, true
}

// handleApplymentUploadURL POST /admin/applyments/upload-url {filename, mime, size}：
// 转发给 svc-file，返回 {fileId, uploadUrl}；浏览器随后把文件 PUT 到 uploadUrl。
func handleApplymentUploadURL(c *gin.Context) {
	if _, ok := requireRole(c, roleOperator); !ok {
		return
	}
	var in struct {
		Filename string `json:"filename"`
		Mime     string `json:"mime"`
		Size     int64  `json:"size"`
	}
	if c.ShouldBindJSON(&in) != nil || in.Filename == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "filename 必填"})
		return
	}
	if in.Size > maxMediaBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "图片不能超过 2MB"})
		return
	}
	body, _ := json.Marshal(map[string]any{"filename": in.Filename, "mime": in.Mime, "size": in.Size, "visibility": "private"})
	code, raw, err := proxyUploadURL(c.Request.Context(), c.GetHeader("Authorization"), body)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "svc-file 不可用: " + err.Error()})
		return
	}
	c.Data(code, "application/json", raw)
}

func registerApplymentRoutes(g *gin.RouterGroup) {
	g.POST("/applyments/upload-url", handleApplymentUploadURL)
	g.POST("/applyments", handleCreateApplyment)
	g.GET("/applyments", handleListApplyments)
	g.GET("/applyments/:id", handleGetApplyment)
	g.PUT("/applyments/:id", handleUpdateApplyment)
	g.POST("/applyments/:id/submit-review", handleSubmitReview)
	g.POST("/applyments/:id/approve", handleApproveApplyment)
	g.POST("/applyments/:id/reject", handleRejectApplyment)
	g.POST("/applyments/:id/submit", handleSubmitApplyment)
	g.POST("/applyments/:id/sync", handleSyncApplyment)
	g.POST("/applyments/:id/cancel", handleCancelApplyment)
}
