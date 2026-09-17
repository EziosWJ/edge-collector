package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/audit"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository owns all MQTT durable state. Capacity checks and command journal
// transitions are deliberately kept here so PostgreSQL and SQLite share the
// same transaction boundary and no caller can accidentally bypass it.
type Repository struct {
	db      *gorm.DB
	secrets *SecretBox
	now     func() time.Time
}

func NewRepository(db *gorm.DB, secrets ...*SecretBox) *Repository {
	var box *SecretBox
	if len(secrets) > 0 {
		box = secrets[0]
	}
	return &Repository{db: db, secrets: box, now: func() time.Time { return time.Now().UTC() }}
}

func (r *Repository) GetConfig(ctx context.Context) (Config, error) {
	var value Config
	err := r.db.WithContext(ctx).Where("id=?", ConfigID).Take(&value).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Config{}, gorm.ErrRecordNotFound
	}
	return value, err
}

// RuntimeConfig decrypts credentials only for the MQTT connector. It never
// returns ciphertext through a management projection.
type RuntimeConfig struct {
	Config
	Password         string
	ClientPrivateKey string
}

func (r *Repository) RuntimeConfig(ctx context.Context) (RuntimeConfig, error) {
	value, err := r.GetConfig(ctx)
	if err != nil {
		return RuntimeConfig{}, err
	}
	result := RuntimeConfig{Config: value}
	if value.PasswordCiphertext != nil && *value.PasswordCiphertext != "" {
		if r.secrets == nil {
			return RuntimeConfig{}, ErrMasterSecretRequired
		}
		result.Password, err = r.secrets.Decrypt(*value.PasswordCiphertext)
		if err != nil {
			return RuntimeConfig{}, fmt.Errorf("decrypt MQTT password: %w", err)
		}
	}
	if value.ClientPrivateKeyCiphertext != nil && *value.ClientPrivateKeyCiphertext != "" {
		if r.secrets == nil {
			return RuntimeConfig{}, ErrMasterSecretRequired
		}
		result.ClientPrivateKey, err = r.secrets.Decrypt(*value.ClientPrivateKeyCiphertext)
		if err != nil {
			return RuntimeConfig{}, fmt.Errorf("decrypt MQTT private key: %w", err)
		}
	}
	return result, nil
}

func (r *Repository) SaveConfig(ctx context.Context, input ConfigInput, event audit.Event) (Config, error) {
	current, err := r.GetConfig(ctx)
	if err != nil {
		return Config{}, err
	}
	next, err := r.configFromInput(current, input)
	if err != nil {
		return Config{}, err
	}
	if err := ValidateConfig(next); err != nil {
		return Config{}, err
	}

	var result Config
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		locked, err := lockConfig(tx)
		if err != nil {
			return err
		}
		// Re-read under the same transaction so two concurrent secret/config
		// writes cannot overwrite each other's kept ciphertext accidentally.
		next, err = r.configFromInput(*locked, input)
		if err != nil {
			return err
		}
		if err := ValidateConfig(next); err != nil {
			return err
		}
		if err := r.ensureConfiguredCapacity(tx, next); err != nil {
			return err
		}
		next.UpdateTime = r.now().UTC()
		updates := configUpdates(next)
		if err := tx.Model(&Config{}).Where("id=?", ConfigID).Updates(updates).Error; err != nil {
			return err
		}
		result = next
		event.Resource = "MQTT 配置"
		event.ResourceID = ConfigID
		return audit.RecordOn(ctx, tx, event)
	})
	return result, err
}

func (r *Repository) configFromInput(current Config, input ConfigInput) (Config, error) {
	if err := validateSecretAction(input.PasswordAction, input.Password, "password"); err != nil {
		return Config{}, err
	}
	if err := validateSecretAction(input.ClientPrivateKeyAction, input.ClientPrivateKey, "clientPrivateKey"); err != nil {
		return Config{}, err
	}
	next := current
	next.Enabled = boolInt(input.Enabled)
	next.EdgeID = strings.TrimSpace(input.EdgeID)
	next.BrokerURL = strings.TrimSpace(input.BrokerURL)
	next.ProtocolVersion = strings.TrimSpace(input.ProtocolVersion)
	next.ClientID = strings.TrimSpace(input.ClientID)
	next.Username = input.Username
	next.TLSEnabled = boolInt(input.TLSEnabled)
	next.CACertificate = input.CACertificate
	next.ClientCertificate = input.ClientCertificate
	next.KeepAliveSeconds = input.KeepAliveSeconds
	next.ConnectTimeoutMS = input.ConnectTimeoutMS
	next.ReconnectMinMS = input.ReconnectMinMS
	next.ReconnectMaxMS = input.ReconnectMaxMS
	next.TopicPrefix = strings.Trim(input.TopicPrefix, "/")
	next.RawPublishIntervalMS = input.RawPublishIntervalMS
	next.OutboxMaxRows = input.OutboxMaxRows
	next.OutboxMaxBytes = input.OutboxMaxBytes
	next.OutboxRetentionDays = input.OutboxRetentionDays
	next.CommandJournalRetentionDays = input.CommandJournalRetentionDays
	next.CommandJournalMaxRows = input.CommandJournalMaxRows
	next.CommandQueueCapacity = input.CommandQueueCapacity
	next.CommandPollFairness = input.CommandPollFairness
	if input.PasswordAction == SecretSet {
		if r.secrets == nil {
			return Config{}, ErrMasterSecretRequired
		}
		ciphertext, err := r.secrets.Encrypt(input.Password)
		if err != nil {
			return Config{}, err
		}
		next.PasswordCiphertext = &ciphertext
	} else if input.PasswordAction == SecretClear {
		next.PasswordCiphertext = nil
	}
	if input.ClientPrivateKeyAction == SecretSet {
		if r.secrets == nil {
			return Config{}, ErrMasterSecretRequired
		}
		ciphertext, err := r.secrets.Encrypt(input.ClientPrivateKey)
		if err != nil {
			return Config{}, err
		}
		next.ClientPrivateKeyCiphertext = &ciphertext
	} else if input.ClientPrivateKeyAction == SecretClear {
		next.ClientPrivateKeyCiphertext = nil
	}
	return next, nil
}

func configUpdates(value Config) map[string]any {
	return map[string]any{
		"enabled": value.Enabled, "edge_id": value.EdgeID, "broker_url": value.BrokerURL,
		"protocol_version": value.ProtocolVersion, "client_id": value.ClientID,
		"username": value.Username, "password_ciphertext": value.PasswordCiphertext,
		"tls_enabled": value.TLSEnabled, "ca_certificate": value.CACertificate,
		"client_certificate":            value.ClientCertificate,
		"client_private_key_ciphertext": value.ClientPrivateKeyCiphertext,
		"keep_alive_seconds":            value.KeepAliveSeconds, "connect_timeout_ms": value.ConnectTimeoutMS,
		"reconnect_min_ms": value.ReconnectMinMS, "reconnect_max_ms": value.ReconnectMaxMS,
		"topic_prefix": value.TopicPrefix, "raw_publish_interval_ms": value.RawPublishIntervalMS,
		"outbox_max_rows": value.OutboxMaxRows, "outbox_max_bytes": value.OutboxMaxBytes,
		"outbox_retention_days":          value.OutboxRetentionDays,
		"command_journal_retention_days": value.CommandJournalRetentionDays,
		"command_journal_max_rows":       value.CommandJournalMaxRows,
		"command_queue_capacity":         value.CommandQueueCapacity,
		"command_poll_fairness":          value.CommandPollFairness, "update_time": value.UpdateTime,
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func lockConfig(tx *gorm.DB) (*Config, error) {
	query := tx.Where("id=?", ConfigID)
	if tx.Dialector.Name() == "postgres" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var value Config
	if err := query.Take(&value).Error; err != nil {
		return nil, err
	}
	return &value, nil
}

func (r *Repository) Enqueue(ctx context.Context, message OutboxMessage) error {
	if err := validateOutboxMessage(message); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		config, err := lockConfig(tx)
		if err != nil {
			return err
		}
		if err := r.ensureMessageCapacity(tx, *config, message.PayloadBytes, 1); err != nil {
			return err
		}
		return tx.Create(&message).Error
	})
}

// AdmissionResult distinguishes a same-payload redelivery from a newly
// admitted command without exposing a duplicate as a persistence error.
type AdmissionResult struct {
	Admitted bool
	Existing *CommandJournal
}

func (r *Repository) LookupCommand(ctx context.Context, commandID, payloadHash string) (*CommandJournal, error) {
	if commandID == "" || payloadHash == "" {
		return nil, ErrInvalidCommandID
	}
	var existing CommandJournal
	err := r.db.WithContext(ctx).Where("command_id=?", commandID).Take(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if existing.PayloadHash != payloadHash {
		return nil, ErrCommandConflict
	}
	return &existing, nil
}

func (r *Repository) ListIncompleteCommands(ctx context.Context) ([]CommandJournal, error) {
	var values []CommandJournal
	if err := r.db.WithContext(ctx).Where("status IN ?", []string{CommandStatusAccepted, CommandStatusRunning}).Order("received_at, command_id").Find(&values).Error; err != nil {
		return nil, err
	}
	return values, nil
}

func (r *Repository) AdmitCommand(ctx context.Context, journal CommandJournal, accepted OutboxMessage, reservation FinalReservation) (AdmissionResult, error) {
	if journal.CommandID == "" || journal.PayloadHash == "" || accepted.MessageID == "" || reservation.CommandID != journal.CommandID {
		return AdmissionResult{}, ErrOutboxMessageInvalid
	}
	if err := validateOutboxMessage(accepted); err != nil {
		return AdmissionResult{}, err
	}
	if accepted.MessageType != OutboxMessageTypeCommandAccepted || accepted.CommandID == nil || *accepted.CommandID != journal.CommandID {
		return AdmissionResult{}, ErrOutboxMessageInvalid
	}
	if reservation.ReservedRows < 1 || reservation.AcceptedBytes != accepted.PayloadBytes || reservation.ReservedBytes < reservation.FinalBytes {
		return AdmissionResult{}, ErrOutboxMessageInvalid
	}
	result := AdmissionResult{}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		config, err := lockConfig(tx)
		if err != nil {
			return err
		}
		var existing CommandJournal
		findErr := tx.Where("command_id=?", journal.CommandID).Take(&existing).Error
		if findErr == nil {
			if existing.PayloadHash != journal.PayloadHash {
				return ErrCommandConflict
			}
			result.Existing = &existing
			return nil
		}
		if !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return findErr
		}
		var journalRows int64
		if err := tx.Model(&CommandJournal{}).Count(&journalRows).Error; err != nil {
			return err
		}
		if journalRows >= int64(config.CommandJournalMaxRows) {
			return ErrCommandJournalCapacity
		}
		plannedBytes := reservation.AcceptedBytes + reservation.FinalBytes
		if err := r.ensureMessageCapacity(tx, *config, plannedBytes, 2); err != nil {
			return ErrReliableResultCapacityExhausted
		}
		if journal.ReceivedAt.IsZero() {
			journal.ReceivedAt = r.now().UTC()
		}
		if reservation.CreatedAt.IsZero() {
			reservation.CreatedAt = r.now().UTC()
		}
		if accepted.CreatedAt.IsZero() {
			accepted.CreatedAt = r.now().UTC()
		}
		if err := tx.Create(&journal).Error; err != nil {
			return err
		}
		// The accepted notice is inserted by this transaction. Only the final
		// result remains reserved after commit; keeping the accepted notice in
		// the reservation would double-count an already durable row.
		reservation.ReservedRows = 1
		reservation.ReservedBytes = reservation.FinalBytes
		if err := tx.Create(&reservation).Error; err != nil {
			return err
		}
		if err := tx.Create(&accepted).Error; err != nil {
			return err
		}
		result.Admitted = true
		return nil
	})
	return result, err
}

// AdmitRejectedCommand records a terminal rejection/expiry and its reliable
// result atomically. Rejections do not need an ACCEPTED notice, but they still
// pass the same outbox capacity gate before becoming visible to callers.
func (r *Repository) AdmitRejectedCommand(ctx context.Context, journal CommandJournal, final OutboxMessage) (AdmissionResult, error) {
	if journal.CommandID == "" || journal.PayloadHash == "" || final.CommandID == nil || *final.CommandID != journal.CommandID || final.MessageType != OutboxMessageTypeCommandResult {
		return AdmissionResult{}, ErrOutboxMessageInvalid
	}
	if journal.Status != CommandStatusRejected && journal.Status != CommandStatusExpired && journal.Status != CommandStatusFailed {
		return AdmissionResult{}, ErrOutboxMessageInvalid
	}
	if err := validateOutboxMessage(final); err != nil {
		return AdmissionResult{}, err
	}
	result := AdmissionResult{}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		config, err := lockConfig(tx)
		if err != nil {
			return err
		}
		var existing CommandJournal
		findErr := tx.Where("command_id=?", journal.CommandID).Take(&existing).Error
		if findErr == nil {
			if existing.PayloadHash != journal.PayloadHash {
				return ErrCommandConflict
			}
			result.Existing = &existing
			return nil
		}
		if !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return findErr
		}
		var journalRows int64
		if err := tx.Model(&CommandJournal{}).Count(&journalRows).Error; err != nil {
			return err
		}
		if journalRows >= int64(config.CommandJournalMaxRows) {
			return ErrCommandJournalCapacity
		}
		if err := r.ensureMessageCapacity(tx, *config, final.PayloadBytes, 1); err != nil {
			return ErrReliableResultCapacityExhausted
		}
		if journal.ReceivedAt.IsZero() {
			journal.ReceivedAt = r.now().UTC()
		}
		if journal.CompletedAt == nil {
			completed := r.now().UTC()
			journal.CompletedAt = &completed
		}
		if final.CreatedAt.IsZero() {
			final.CreatedAt = r.now().UTC()
		}
		journal.ResultPayload = final.Payload
		if err := tx.Create(&journal).Error; err != nil {
			return err
		}
		if err := tx.Create(&final).Error; err != nil {
			return err
		}
		result.Admitted = true
		return nil
	})
	return result, err
}

// RequeueStoredFinal reconstructs a durable result row for a duplicate
// terminal delivery after the previous row was already PUBACKed and deleted.
// It never creates an execution token or touches the device runtime.
func (r *Repository) RequeueStoredFinal(ctx context.Context, commandID, topic string) error {
	if commandID == "" || topic == "" {
		return ErrInvalidCommandID
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		config, err := lockConfig(tx)
		if err != nil {
			return err
		}
		var journal CommandJournal
		if err := tx.Where("command_id=?", commandID).Take(&journal).Error; err != nil {
			return err
		}
		if !isTerminalCommandStatus(journal.Status) || journal.ResultPayload == "" {
			return nil
		}
		var existing int64
		if err := tx.Model(&OutboxMessage{}).Where("command_id=? AND message_type=?", commandID, OutboxMessageTypeCommandResult).Count(&existing).Error; err != nil {
			return err
		}
		if existing > 0 {
			return nil
		}
		var envelope Envelope
		if err := json.Unmarshal([]byte(journal.ResultPayload), &envelope); err != nil {
			return ErrOutboxMessageInvalid
		}
		now := r.now().UTC()
		envelope.MessageID = NewMessageID(now)
		payload, err := json.Marshal(envelope)
		if err != nil {
			return ErrOutboxMessageInvalid
		}
		command := commandID
		expiresAt := now.Add(time.Duration(config.OutboxRetentionDays) * 24 * time.Hour)
		final := OutboxMessage{MessageID: envelope.MessageID, MessageType: OutboxMessageTypeCommandResult, CommandID: &command, Topic: topic, QoS: 1, Retain: 0, Payload: string(payload), PayloadBytes: int64(len(payload)), Priority: OutboxPriorityCommandFinal, CreatedAt: now, ExpiresAt: &expiresAt}
		if err := r.ensureMessageCapacity(tx, *config, final.PayloadBytes, 1); err != nil {
			return ErrReliableResultCapacityExhausted
		}
		return tx.Create(&final).Error
	})
}

// MarkCommandRunning is idempotent and only transitions an admitted command.
func (r *Repository) MarkCommandRunning(ctx context.Context, commandID string, startedAt time.Time) (bool, error) {
	if commandID == "" {
		return false, ErrInvalidCommandID
	}
	if startedAt.IsZero() {
		startedAt = r.now().UTC()
	}
	result := r.db.WithContext(ctx).Model(&CommandJournal{}).Where("command_id=? AND status=?", commandID, CommandStatusAccepted).Updates(map[string]any{
		"status": CommandStatusRunning, "started_at": startedAt.UTC(),
	})
	return result.RowsAffected == 1, result.Error
}

var ErrInvalidCommandID = errors.New("MQTT_COMMAND_ID_INVALID")

// FinalizeCommand atomically stores the terminal journal state and the
// high-priority reliable result. The reservation is the admission proof; it is
// released in the same transaction as the durable final row.
func (r *Repository) FinalizeCommand(ctx context.Context, journal CommandJournal, final OutboxMessage) error {
	if journal.CommandID == "" || final.CommandID == nil || *final.CommandID != journal.CommandID || final.MessageType != OutboxMessageTypeCommandResult {
		return ErrOutboxMessageInvalid
	}
	if err := validateOutboxMessage(final); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		config, err := lockConfig(tx)
		if err != nil {
			return err
		}
		var current CommandJournal
		if err := tx.Where("command_id=?", journal.CommandID).Take(&current).Error; err != nil {
			return err
		}
		if isTerminalCommandStatus(current.Status) {
			return nil
		}
		var reservation FinalReservation
		if err := tx.Where("command_id=?", journal.CommandID).Take(&reservation).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrReservationNotFound
			}
			return err
		}
		if final.PayloadBytes > reservation.FinalBytes {
			return ErrReliableResultCapacityExhausted
		}
		if err := tx.Delete(&FinalReservation{}, "command_id=?", journal.CommandID).Error; err != nil {
			return err
		}
		if err := r.ensureMessageCapacity(tx, *config, final.PayloadBytes, 1); err != nil {
			return ErrReliableResultCapacityExhausted
		}
		if final.CreatedAt.IsZero() {
			final.CreatedAt = r.now().UTC()
		}
		if err := tx.Create(&final).Error; err != nil {
			return err
		}
		updates := map[string]any{
			"status": journal.Status, "started_at": journal.StartedAt,
			"completed_at": journal.CompletedAt, "result_payload": journal.ResultPayload,
			"error_type": journal.ErrorType, "error_message": journal.ErrorMessage,
		}
		if err := tx.Model(&CommandJournal{}).Where("command_id=?", journal.CommandID).Updates(updates).Error; err != nil {
			return err
		}
		return nil
	})
}

func (r *Repository) ReleaseReservation(ctx context.Context, commandID string) error {
	if commandID == "" {
		return ErrInvalidCommandID
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockConfig(tx); err != nil {
			return err
		}
		return tx.Delete(&FinalReservation{}, "command_id=?", commandID).Error
	})
}

func (r *Repository) NextOutbox(ctx context.Context, now time.Time) (*OutboxMessage, error) {
	if now.IsZero() {
		now = r.now().UTC()
	}
	var value OutboxMessage
	err := r.db.WithContext(ctx).Where("expires_at IS NULL OR expires_at > ?", now.UTC()).Order("priority DESC, created_at, id").First(&value).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func (r *Repository) MarkOutboxAttempt(ctx context.Context, id int64, at time.Time, errValue error) error {
	if at.IsZero() {
		at = r.now().UTC()
	}
	updates := map[string]any{"attempt_count": gorm.Expr("attempt_count + 1"), "last_attempt_at": at.UTC()}
	if errValue == nil {
		updates["last_error"] = nil
	} else {
		message := errValue.Error()
		updates["last_error"] = message
	}
	return r.db.WithContext(ctx).Model(&OutboxMessage{}).Where("id=?", id).Updates(updates).Error
}

func (r *Repository) DeleteOutbox(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&OutboxMessage{}, "id=?", id).Error
}

func (r *Repository) OutboxStats(ctx context.Context) (OutboxStats, error) {
	var stats OutboxStats
	if err := r.db.WithContext(ctx).Model(&OutboxMessage{}).Count(&stats.Rows).Error; err != nil {
		return stats, err
	}
	if err := r.db.WithContext(ctx).Model(&OutboxMessage{}).Select("COALESCE(SUM(payload_bytes), 0)").Scan(&stats.Bytes).Error; err != nil {
		return stats, err
	}
	var oldestRow OutboxMessage
	if err := r.db.WithContext(ctx).Select("created_at").Order("created_at, id").First(&oldestRow).Error; err == nil && !oldestRow.CreatedAt.IsZero() {
		oldest := oldestRow.CreatedAt
		stats.OldestAt = &oldest
	}
	var lastError string
	if err := r.db.WithContext(ctx).Model(&OutboxMessage{}).Where("last_error IS NOT NULL AND last_error <> ''").Order("last_attempt_at DESC, id DESC").Limit(1).Pluck("last_error", &lastError).Error; err == nil && lastError != "" {
		stats.LastError = &lastError
	}
	return stats, nil
}

func (r *Repository) Cleanup(ctx context.Context, now time.Time) error {
	if now.IsZero() {
		now = r.now().UTC()
	}
	if err := r.db.WithContext(ctx).Where("expires_at IS NOT NULL AND expires_at <= ? AND (message_type <> ? OR EXISTS (SELECT 1 FROM mqtt_command_journal AS journal WHERE journal.command_id=mqtt_outbox.command_id AND journal.status IN (?, ?, ?, ?)))", now.UTC(), OutboxMessageTypeCommandResult, CommandStatusRejected, CommandStatusExpired, CommandStatusSucceeded, CommandStatusFailed).Delete(&OutboxMessage{}).Error; err != nil {
		return err
	}
	config, err := r.GetConfig(ctx)
	if err != nil {
		return err
	}
	cutoff := now.UTC().Add(-time.Duration(config.CommandJournalRetentionDays) * 24 * time.Hour)
	return r.db.WithContext(ctx).Where("received_at < ? AND status IN (?, ?, ?, ?)", cutoff, CommandStatusRejected, CommandStatusExpired, CommandStatusSucceeded, CommandStatusFailed).Delete(&CommandJournal{}).Error
}

func (r *Repository) FindCommand(ctx context.Context, commandID string) (*CommandJournalView, error) {
	var value CommandJournal
	if err := r.db.WithContext(ctx).Where("command_id=?", commandID).Take(&value).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, gorm.ErrRecordNotFound
		}
		return nil, err
	}
	view := commandJournalView(value)
	return &view, nil
}

func (r *Repository) PageCommands(ctx context.Context, query CommandJournalQuery) (Page[CommandJournalView], error) {
	if query.Page < 1 {
		query.Page = 1
	}
	if query.PageSize < 1 {
		query.PageSize = 20
	}
	if query.PageSize > 500 {
		query.PageSize = 500
	}
	db := r.db.WithContext(ctx).Model(&CommandJournal{})
	if query.Status != "" {
		db = db.Where("status=?", query.Status)
	}
	if query.DeviceID != "" {
		db = db.Where("device_id=?", query.DeviceID)
	}
	var page Page[CommandJournalView]
	if err := db.Count(&page.Total).Error; err != nil {
		return page, err
	}
	page.Page, page.PageSize = query.Page, query.PageSize
	var records []CommandJournal
	if err := db.Order("received_at DESC, command_id").Offset((query.Page - 1) * query.PageSize).Limit(query.PageSize).Find(&records).Error; err != nil {
		return page, err
	}
	page.Records = make([]CommandJournalView, 0, len(records))
	for _, record := range records {
		page.Records = append(page.Records, commandJournalView(record))
	}
	return page, nil
}

func commandJournalView(value CommandJournal) CommandJournalView {
	return CommandJournalView{CommandID: value.CommandID, DeviceID: value.DeviceID, CommandName: value.CommandName, Status: value.Status, ReceivedAt: value.ReceivedAt, IssuedAt: value.IssuedAt, ExpiresAt: value.ExpiresAt, StartedAt: value.StartedAt, CompletedAt: value.CompletedAt, ErrorType: value.ErrorType, ErrorMessage: value.ErrorMessage}
}

func validateOutboxMessage(message OutboxMessage) error {
	if message.MessageID == "" || message.MessageType == "" || message.Topic == "" || message.PayloadBytes < 0 || message.PayloadBytes != int64(len([]byte(message.Payload))) || (message.QoS != 0 && message.QoS != 1) || (message.Retain != 0 && message.Retain != 1) {
		return ErrOutboxMessageInvalid
	}
	return nil
}

func (r *Repository) ensureMessageCapacity(tx *gorm.DB, config Config, addedBytes int64, addedRows int) error {
	var rows int64
	if err := tx.Model(&OutboxMessage{}).Count(&rows).Error; err != nil {
		return err
	}
	var bytes int64
	if err := tx.Model(&OutboxMessage{}).Select("COALESCE(SUM(payload_bytes), 0)").Scan(&bytes).Error; err != nil {
		return err
	}
	var reservedRows int64
	if err := tx.Model(&FinalReservation{}).Select("COALESCE(SUM(reserved_rows), 0)").Scan(&reservedRows).Error; err != nil {
		return err
	}
	var reservedBytes int64
	if err := tx.Model(&FinalReservation{}).Select("COALESCE(SUM(reserved_bytes), 0)").Scan(&reservedBytes).Error; err != nil {
		return err
	}
	if rows+reservedRows+int64(addedRows) > int64(config.OutboxMaxRows) || bytes+reservedBytes+addedBytes > config.OutboxMaxBytes {
		return ErrReliableResultCapacityExhausted
	}
	return nil
}

func (r *Repository) ensureConfiguredCapacity(tx *gorm.DB, config Config) error {
	return r.ensureMessageCapacity(tx, config, 0, 0)
}
