package index

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Pin retains the current root generation until ttl expires. Reservations
// survive reopening; capacity exhaustion never silently evicts active pins.
func (s *Store) Pin(ctx context.Context, rootID string, ttl time.Duration) (snapshot Snapshot, err error) {
	if !validRoot(rootID) || ttl <= 0 || ttl > s.options.MaxPinTTL {
		return Snapshot{}, fmt.Errorf("index: invalid root or retention lifetime")
	}
	ctx, cancel := s.context(ctx)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Snapshot{}, storeError("begin retention reservation", err)
	}
	defer rollbackTransaction(tx, &err)
	now := time.Now()
	if _, err = tx.ExecContext(ctx, "DELETE FROM pins WHERE expires<=?", now.UnixNano()); err != nil {
		return Snapshot{}, storeError("expire reservations", err)
	}
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM pins").Scan(&count); err != nil {
		return Snapshot{}, storeError("count reservations", err)
	}
	if count >= s.options.MaxPins {
		return Snapshot{}, ErrCapacity
	}
	if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO roots(root) VALUES (?)", rootID); err != nil {
		return Snapshot{}, storeError("initialize root", err)
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return Snapshot{}, fmt.Errorf("index: reservation identity: %w", err)
	}
	snapshot = Snapshot{ID: base64.RawURLEncoding.EncodeToString(nonce[:]), RootID: rootID, ExpiresAt: now.Add(ttl)}
	if err = tx.QueryRowContext(ctx, "SELECT generation FROM roots WHERE root=?", rootID).Scan(&snapshot.Generation); err != nil {
		return Snapshot{}, storeError("read retained generation", err)
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO pins(id,root,generation,expires) VALUES (?,?,?,?)", snapshot.ID, rootID, snapshot.Generation, snapshot.ExpiresAt.UnixNano()); err != nil {
		return Snapshot{}, storeError("reserve generation", err)
	}
	return snapshot, commitTransaction(tx)
}

// Release idempotently drops one retention reservation.
func (s *Store) Release(ctx context.Context, reservationID string) error {
	return s.ReleaseMany(ctx, []string{reservationID})
}

// ReleaseMany drops only explicitly owned reservations in one bounded write.
// It never clears all pins merely because a process is shutting down.
func (s *Store) ReleaseMany(ctx context.Context, reservationIDs []string) error {
	if len(reservationIDs) > s.options.MaxPins {
		return ErrTooLarge
	}
	if len(reservationIDs) == 0 {
		return nil
	}
	args := make([]any, len(reservationIDs))
	for i, id := range reservationIDs {
		if len(id) > maxIDBytes {
			return ErrTooLarge
		}
		args[i] = id
	}
	ctx, cancel := s.context(ctx)
	defer cancel()
	statement := "DELETE FROM pins WHERE id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(args)), ",") + ")"
	if _, err := s.db.ExecContext(ctx, statement, args...); err != nil {
		return storeError("release reservation", err)
	}
	return nil
}

func validateSnapshot(ctx context.Context, tx *sql.Tx, snapshot Snapshot) error {
	var root string
	var generation uint64
	var expires int64
	err := tx.QueryRowContext(ctx, "SELECT root,generation,expires FROM pins WHERE id=?", snapshot.ID).Scan(&root, &generation, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrStale
	}
	if err != nil {
		return storeError("read reservation", err)
	}
	if root != snapshot.RootID || generation != snapshot.Generation || expires != snapshot.ExpiresAt.UnixNano() || expires <= time.Now().UnixNano() {
		return ErrStale
	}
	return nil
}

// LookupKey recovers a full ordering position from a bounded cursor handle.
// It does not disclose file or symbol data; publication still needs authority checks.
func (s *Store) LookupKey(ctx context.Context, snapshot Snapshot, symbolID string, version uint64) (key Key, err error) {
	ctx, cancel := s.context(ctx)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Key{}, storeError("begin key lookup", err)
	}
	defer rollbackTransaction(tx, &err)
	if err = validateSnapshot(ctx, tx, snapshot); err != nil {
		return Key{}, err
	}
	var name []byte
	err = tx.QueryRowContext(ctx, `SELECT path,name,start_byte,kind,id,version FROM symbols WHERE root=? AND id=? AND version=?`, snapshot.RootID, symbolID, version).
		Scan(&key.Path, &name, &key.StartByte, &key.Kind, &key.SymbolID, &key.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return Key{}, ErrStale
	}
	if err != nil {
		return Key{}, storeError("lookup ordering key", err)
	}
	key.Name = string(name)
	return key, commitTransaction(tx)
}

func prefixEnd(prefix []byte) []byte {
	end := append([]byte(nil), prefix...)
	for i := len(end) - 1; i >= 0; i-- {
		if end[i] != 0xff {
			end[i]++
			return end[:i+1]
		}
	}
	return nil
}

// scanSQL uses only a leading-key range and a row-value keyset seek.
// Generation and scope predicates intentionally stay out: hidden rejection
// inside SQL could examine an arbitrary number of obsolete or unrelated rows.
func scanSQL(root string, query Query, after *Key, limit int) (string, []any, error) {
	if query.Order == "" {
		query.Order = PathOrder
	}
	if query.Order != PathOrder && query.Order != NameOrder {
		return "", nil, fmt.Errorf("index: unsupported scan order")
	}
	if (query.NameMatch != MatchAny && query.NameMatch != MatchExact && query.NameMatch != MatchPrefix) ||
		(query.PathMatch != MatchAny && query.PathMatch != MatchExact && query.PathMatch != MatchPrefix) ||
		(query.NameMatch == MatchPrefix && query.Order != NameOrder) ||
		(query.PathMatch != MatchAny && (query.Order != PathOrder || query.NameMatch != MatchAny)) {
		return "", nil, fmt.Errorf("index: query is not a supported leading-index range")
	}
	indexName := "symbols_path"
	columns := "path,start_byte,kind,id,version"
	args := []any{root}
	where := []string{"root=?"}
	if query.Order == NameOrder || query.NameMatch != MatchAny {
		indexName = "symbols_name"
		columns = "name,path,start_byte,kind,id,version"
	}
	if query.NameMatch == MatchExact {
		columns = "path,start_byte,kind,id,version"
	}
	if query.PathMatch == MatchExact {
		columns = "start_byte,kind,id,version"
	}
	if after != nil && ((query.NameMatch == MatchExact && after.Name != query.Name) ||
		(query.NameMatch == MatchPrefix && !strings.HasPrefix(after.Name, query.Name)) ||
		(query.PathMatch == MatchExact && !bytes.Equal(after.Path, query.Path)) ||
		(query.PathMatch == MatchPrefix && !bytes.HasPrefix(after.Path, query.Path))) {
		return "", nil, fmt.Errorf("index: continuation key lies outside its query range")
	}
	addRange := func(column string, match Match, value []byte) {
		switch match {
		case MatchExact:
			where = append(where, column+"=?")
			args = append(args, value)
		case MatchPrefix:
			// Once a key is inside the prefix, its tuple seek is the lower
			// bound. Keeping a competing scalar lower bound makes SQLite
			// scan the prefix again and filter prior records on every page.
			if after == nil {
				where = append(where, column+">=?")
				args = append(args, value)
			}
			if end := prefixEnd(value); end != nil {
				where = append(where, column+"<?")
				args = append(args, end)
			}
		case MatchAny:
		}
	}
	addRange("name", query.NameMatch, []byte(query.Name))
	addRange("path", query.PathMatch, query.Path)
	if after != nil {
		keyArgs := make([]any, 0, 6)
		if indexName == "symbols_name" && query.NameMatch != MatchExact {
			keyArgs = append(keyArgs, []byte(after.Name))
		}
		if query.PathMatch != MatchExact {
			keyArgs = append(keyArgs, after.Path)
		}
		keyArgs = append(keyArgs, after.StartByte, after.Kind, after.SymbolID, after.Version)
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(keyArgs)), ",")
		where = append(where, "("+columns+")>("+placeholders+")")
		args = append(args, keyArgs...)
	}
	args = append(args, limit)
	return "SELECT id,version,size FROM symbols INDEXED BY " + indexName + " WHERE " + strings.Join(where, " AND ") + " ORDER BY " + columns + " LIMIT ?", args, nil
}

type recordHeader struct {
	id      string
	version uint64
	size    int64
	until   uint64
}

// Scan retrieves a bounded slice of raw versioned records. Callers must check
// Record.Visible and their scope/authority policy after this bounded read.
// A too-small byte budget leaves the unconsumed candidate in place; Next
// always identifies the last returned record, never a peeked candidate.
func (s *Store) Scan(ctx context.Context, snapshot Snapshot, query Query, after *Key, limit int, byteBudget int64) (page Page, err error) {
	if limit < 1 || limit > maxScanRecords || byteBudget < 1 || byteBudget > 16<<20 {
		return Page{}, ErrTooLarge
	}
	// Headers contain a bounded handle and two integers. Reserve their
	// maximum size before allocating a batch or retrieving any row payload.
	headerLimit := min(limit, int(byteBudget/(maxIDBytes+16)))
	if headerLimit < 1 {
		return Page{}, ErrTooLarge
	}
	statement, args, err := scanSQL(snapshot.RootID, query, after, headerLimit)
	if err != nil {
		return Page{}, err
	}
	ctx, cancel := s.context(ctx)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Page{}, storeError("begin scan", err)
	}
	defer rollbackTransaction(tx, &err)
	if err = validateSnapshot(ctx, tx, snapshot); err != nil {
		return Page{}, err
	}
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return Page{}, storeError("read scan headers", err)
	}
	headers := make([]recordHeader, 0, headerLimit)
	for rows.Next() {
		var header recordHeader
		if err = rows.Scan(&header.id, &header.version, &header.size); err != nil {
			break
		}
		page.ReadBytes += int64(len(header.id)) + 16
		page.Examined++
		if header.size < 0 || header.size > int64(s.options.MaxRecordBytes) {
			err = ErrTooLarge
			break
		}
		headers = append(headers, header)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return Page{}, storeError("read scan headers", err)
	}
	page.Exhausted = len(headers) < headerLimit
	for _, header := range headers {
		if header.size > byteBudget-page.ReadBytes {
			page.Exhausted = false
			break
		}
		var data []byte
		if err = tx.QueryRowContext(ctx, "SELECT data,until_version FROM symbols WHERE root=? AND id=? AND version=?", snapshot.RootID, header.id, header.version).Scan(&data, &header.until); err != nil {
			return Page{}, storeError("read declaration", err)
		}
		page.ReadBytes += int64(len(data))
		var source sourceRecord
		if err = json.Unmarshal(data, &source); err != nil {
			return Page{}, fmt.Errorf("index: decode declaration: %w", err)
		}
		key := Key{Path: source.File.Path, Name: source.Symbol.Name, StartByte: source.Symbol.StartByte, Kind: source.Symbol.Kind, SymbolID: header.id, Version: header.version}
		page.Records = append(page.Records, Record{File: source.File, Symbol: source.Symbol, Key: key, ValidFrom: header.version, ValidUntil: header.until})
		page.Next = &key
	}
	return page, commitTransaction(tx)
}

// GC deletes at most limit obsolete declarations and limit obsolete sources.
// A root with an active pin retains all raw ordering keys: even an obsolete
// record may be a cursor's last consumed key. The caller schedules these
// batches after release/expiry; capacity never silently invalidates a pin.
// SQLite reuses freed pages without a full VACUUM.
func (s *Store) GC(ctx context.Context, rootID string, limit int) (deleted int64, err error) {
	if limit < 1 || limit > maxScanRecords {
		return 0, ErrTooLarge
	}
	ctx, cancel := s.context(ctx)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, storeError("begin version collection", err)
	}
	defer rollbackTransaction(tx, &err)
	var generation uint64
	if err = tx.QueryRowContext(ctx, "SELECT generation FROM roots WHERE root=?", rootID).Scan(&generation); errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	} else if err != nil {
		return 0, storeError("read collection generation", err)
	}
	var active int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM pins WHERE root=? AND expires>?", rootID, time.Now().UnixNano()).Scan(&active); err != nil {
		return 0, storeError("read active reservations", err)
	}
	if active > 0 {
		return 0, nil
	}
	for _, table := range []string{"symbols", "files"} {
		statement := "DELETE FROM " + table + " WHERE (root,id,version) IN (SELECT root,id,version FROM " + table + " INDEXED BY " + table + "_gc WHERE root=? AND until_version>0 AND until_version<=? ORDER BY until_version,id,version LIMIT ?)"
		result, execErr := tx.ExecContext(ctx, statement, rootID, generation, limit)
		if execErr != nil {
			return 0, storeError("collect obsolete versions", execErr)
		}
		count, countErr := result.RowsAffected()
		if countErr != nil {
			return 0, storeError("count collected versions", countErr)
		}
		deleted += count
	}
	return deleted, commitTransaction(tx)
}
