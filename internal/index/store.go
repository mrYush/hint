package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mrYush/hint/internal/pathkey"
	"modernc.org/sqlite"
)

const (
	maxScanRecords = 4096
	maxIDBytes     = 128
	pageBytes      = 4096
)

// Store owns one SQLite connection. Every operation has a deadline and ends
// its transaction before returning; snapshots retain versions, not transactions.
type Store struct {
	db      *sql.DB
	options Options
}

// Options returns the normalized immutable budgets used by this store.
func (s *Store) Options() Options { return s.options }

const schema = `
CREATE TABLE IF NOT EXISTS roots (
 root TEXT PRIMARY KEY, generation INTEGER NOT NULL DEFAULT 0,
 files INTEGER NOT NULL DEFAULT 0, pending INTEGER NOT NULL DEFAULT 0,
 indexed INTEGER NOT NULL DEFAULT 0, omitted INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS files (
 root TEXT NOT NULL, id TEXT NOT NULL, version INTEGER NOT NULL,
 until_version INTEGER NOT NULL DEFAULT 0, data BLOB NOT NULL,
 PRIMARY KEY(root,id,version)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS files_gc ON files(root,until_version,id,version);
CREATE INDEX IF NOT EXISTS files_current ON files(root,id,until_version);
CREATE TABLE IF NOT EXISTS heads (
 root TEXT NOT NULL, id TEXT NOT NULL, version INTEGER NOT NULL,
 path BLOB NOT NULL, parent BLOB NOT NULL,
 PRIMARY KEY(root,id)
) WITHOUT ROWID;
CREATE UNIQUE INDEX IF NOT EXISTS heads_path ON heads(root,path);
CREATE INDEX IF NOT EXISTS heads_parent ON heads(root,parent,path,id);
CREATE TABLE IF NOT EXISTS symbols (
 root TEXT NOT NULL, id TEXT NOT NULL, version INTEGER NOT NULL,
 file TEXT NOT NULL, path BLOB NOT NULL, name BLOB NOT NULL,
 start_byte INTEGER NOT NULL, kind TEXT NOT NULL,
 until_version INTEGER NOT NULL DEFAULT 0, data BLOB NOT NULL, size INTEGER NOT NULL,
 PRIMARY KEY(root,id,version)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS symbols_path ON symbols(root,path,start_byte,kind,id,version);
CREATE INDEX IF NOT EXISTS symbols_name ON symbols(root,name,path,start_byte,kind,id,version);
CREATE INDEX IF NOT EXISTS symbols_file ON symbols(root,file,until_version);
CREATE INDEX IF NOT EXISTS symbols_gc ON symbols(root,until_version,id,version);
CREATE TABLE IF NOT EXISTS pins (
 id TEXT PRIMARY KEY, root TEXT NOT NULL, generation INTEGER NOT NULL, expires INTEGER NOT NULL
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS pins_root ON pins(root,generation,expires);
CREATE INDEX IF NOT EXISTS pins_expiry ON pins(expires);
CREATE TABLE IF NOT EXISTS frontier (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT, root TEXT NOT NULL, path BLOB NOT NULL,
 revision INTEGER NOT NULL DEFAULT 1, not_before INTEGER NOT NULL DEFAULT 0,
 UNIQUE(root,path)
);
CREATE INDEX IF NOT EXISTS frontier_root ON frontier(root,sequence);
CREATE INDEX IF NOT EXISTS frontier_ready ON frontier(root,not_before,sequence);
CREATE TABLE IF NOT EXISTS policy_epochs (
 root TEXT PRIMARY KEY, epoch INTEGER NOT NULL
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS policy_files (
 root TEXT NOT NULL, path BLOB NOT NULL, fingerprint TEXT NOT NULL,
 PRIMARY KEY(root,path)
) WITHOUT ROWID;
PRAGMA user_version=3;
`

// Open creates or reopens a private catalog with finite cache and disk limits.
// MaxBytes covers the database and its rollback journal conservatively: only
// one third minus fixed slack is admitted as database pages. DELETE journaling
// avoids accumulating WAL between requests. Queries never require temp sorts.
func Open(path string, options Options) (*Store, error) {
	options, err := normalizeOptions(options)
	if err != nil {
		return nil, err
	}
	if path == "" || path == ":memory:" {
		return nil, fmt.Errorf("index: a durable database path is required")
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("index: resolve database path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("index: create catalog directory: %w", err)
	}
	var existingBytes int64
	for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		info, err := os.Stat(path + suffix)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("index: inspect existing catalog: %w", err)
		}
		if err == nil {
			existingBytes += info.Size()
		}
	}
	if existingBytes > options.MaxBytes {
		return nil, ErrCapacity
	}
	pages := (options.MaxBytes - 64*1024) / (3 * pageBytes)
	q := url.Values{}
	for _, pragma := range []string{
		"page_size(4096)", "journal_mode(DELETE)", "synchronous(FULL)", "temp_store(MEMORY)", "mmap_size(0)",
		"busy_timeout(1000)", fmt.Sprintf("cache_size(-%d)", options.CacheKiB),
		fmt.Sprintf("max_page_count(%d)", pages),
	} {
		q.Add("_pragma", pragma)
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: q.Encode()}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("index: open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db, options: options}
	ctx, cancel := s.context(context.Background())
	defer cancel()
	var version, actualPages, actualPageBytes int64
	if err = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err == nil && version != 0 && version != 2 && version != 3 {
		err = fmt.Errorf("index: unsupported catalog schema version %d", version)
	}
	if err == nil {
		err = db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&actualPageBytes)
		if err == nil && actualPageBytes != pageBytes {
			err = fmt.Errorf("index: unsupported SQLite page size %d", actualPageBytes)
		}
	}
	if err == nil {
		err = db.QueryRowContext(ctx, "PRAGMA max_page_count").Scan(&actualPages)
		if err == nil && actualPages > pages {
			err = ErrCapacity
		}
	}
	if err == nil {
		err = s.initializeSchema(ctx, version)
	}
	if err == nil {
		err = os.Chmod(path, 0o600)
	}
	if err != nil {
		return nil, errors.Join(storeError("initialize catalog", err), db.Close())
	}
	return s, nil
}

func (s *Store) initializeSchema(ctx context.Context, version int64) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackTransaction(tx, &err)
	if version == 2 {
		if _, err = tx.ExecContext(ctx, "ALTER TABLE frontier ADD COLUMN not_before INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, schema); err != nil {
		return err
	}
	return commitTransaction(tx)
}

func normalizeOptions(o Options) (Options, error) {
	if o.MaxBytes == 0 {
		o.MaxBytes = 128 << 20
	}
	if o.CacheKiB == 0 {
		o.CacheKiB = 2048
	}
	if o.MaxRecordBytes == 0 {
		o.MaxRecordBytes = 64 << 10
	}
	if o.MaxSourceBytes == 0 {
		o.MaxSourceBytes = 4 << 20
	}
	if o.MaxSourceSymbols == 0 {
		o.MaxSourceSymbols = 4096
	}
	if o.MaxPins == 0 {
		o.MaxPins = 128
	}
	if o.MaxPinTTL == 0 {
		o.MaxPinTTL = 10 * time.Minute
	}
	if o.OperationTimeout == 0 {
		o.OperationTimeout = 2 * time.Second
	}
	if o.MaxBytes < 512<<10 || o.CacheKiB < 16 || o.CacheKiB > 64<<10 ||
		o.MaxRecordBytes < 1024 || o.MaxRecordBytes > 1<<20 ||
		o.MaxSourceBytes < o.MaxRecordBytes || o.MaxSourceBytes > 16<<20 ||
		o.MaxSourceSymbols < 1 || o.MaxSourceSymbols > 16384 ||
		o.MaxPins < 1 || o.MaxPins > 128 || o.MaxPinTTL <= 0 || o.MaxPinTTL > 24*time.Hour ||
		o.OperationTimeout <= 0 || o.OperationTimeout > time.Minute {
		return o, fmt.Errorf("index: options exceed supported finite resource limits")
	}
	return o, nil
}

// Close releases the database connection after callers have stopped their work.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) context(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, s.options.OperationTimeout)
}

func storeError(operation string, err error) error {
	var sqliteError *sqlite.Error
	if errors.As(err, &sqliteError) && sqliteError.Code()&0xff == 13 {
		err = errors.Join(ErrCapacity, err)
	}
	return fmt.Errorf("index: %s: %w", operation, err)
}

type sourceRecord struct {
	File   File
	Symbol Symbol
}

func (s *Store) encode(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("index: encode record: %w", err)
	}
	if len(data) > s.options.MaxRecordBytes {
		return nil, ErrTooLarge
	}
	return data, nil
}

func validRoot(root string) bool {
	return len(root) > 0 && len(root) <= maxIDBytes && utf8.ValidString(root) && !strings.ContainsRune(root, '\x00')
}

func validPath(path []byte) bool {
	return pathkey.Valid(string(path))
}

func parentPath(path []byte) []byte {
	if i := strings.LastIndexByte(string(path), '/'); i >= 0 {
		return path[:i]
	}
	return []byte{}
}

func (s *Store) prepareSource(file File, symbols []Symbol) (File, []byte, [][]byte, []Symbol, error) {
	if len(file.Path)+len(file.Hash)+len(file.Policy)+len(file.Reason) > s.options.MaxRecordBytes {
		return File{}, nil, nil, nil, ErrTooLarge
	}
	if !validRoot(file.RootID) || !validPath(file.Path) || file.Size < 0 {
		return File{}, nil, nil, nil, fmt.Errorf("index: invalid source identity, relative path or size")
	}
	if len(symbols) > s.options.MaxSourceSymbols {
		return File{}, nil, nil, nil, ErrTooLarge
	}
	if file.ID == "" {
		file.ID = EntryID(file.RootID, string(file.Path))
	}
	if file.ID != EntryID(file.RootID, string(file.Path)) {
		return File{}, nil, nil, nil, fmt.Errorf("index: source handle does not match its root and path")
	}
	if file.Kind != KindFile && file.Kind != KindDirectory && file.Kind != KindBoundary {
		return File{}, nil, nil, nil, fmt.Errorf("index: invalid entry kind %q", file.Kind)
	}
	if file.State != StatePending && file.State != StateIndexed && file.State != StateOmitted {
		return File{}, nil, nil, nil, fmt.Errorf("index: invalid source state %q", file.State)
	}
	if len(symbols) > 0 && (file.Kind != KindFile || file.State != StateIndexed) {
		return File{}, nil, nil, nil, fmt.Errorf("index: only indexed files may carry declarations")
	}
	fileData, err := s.encode(file)
	if err != nil {
		return File{}, nil, nil, nil, err
	}
	prepared := append([]Symbol(nil), symbols...)
	data := make([][]byte, 0, len(symbols))
	total := len(fileData)
	seen := make(map[string]bool, len(symbols))
	for i := range prepared {
		symbol := &prepared[i]
		if len(symbol.ID) > maxIDBytes || len(symbol.Name)+len(symbol.Kind)+len(symbol.Signature)+len(symbol.Enclosing) > s.options.MaxRecordBytes {
			return File{}, nil, nil, nil, ErrTooLarge
		}
		if symbol.FileID == "" {
			symbol.FileID = file.ID
		}
		if symbol.ID == "" {
			symbol.ID = SymbolID(file.ID, symbol.StartByte, symbol.Kind, symbol.Name)
		}
		if symbol.ID != SymbolID(file.ID, symbol.StartByte, symbol.Kind, symbol.Name) || symbol.FileID != file.ID || seen[symbol.ID] || !utf8.ValidString(symbol.ID) || !utf8.ValidString(symbol.Name) || !utf8.ValidString(symbol.Kind) ||
			symbol.StartByte < 0 || symbol.EndByte < symbol.StartByte || symbol.EndByte > file.Size ||
			symbol.StartLine < 1 || symbol.EndLine < symbol.StartLine || symbol.Name == "" || symbol.Kind == "" {
			return File{}, nil, nil, nil, fmt.Errorf("index: invalid or duplicate declaration %q", symbol.ID)
		}
		seen[symbol.ID] = true
		encoded, err := s.encode(sourceRecord{File: file, Symbol: *symbol})
		if err != nil {
			return File{}, nil, nil, nil, err
		}
		total += len(encoded)
		if total > s.options.MaxSourceBytes {
			return File{}, nil, nil, nil, ErrTooLarge
		}
		data = append(data, encoded)
	}
	return file, fileData, data, prepared, nil
}

// PutSource atomically replaces current metadata and declarations, increments
// the root generation, and retains older versions until bounded GC is safe.
// Oversized sources fail before opening the write transaction.
func (s *Store) PutSource(ctx context.Context, file File, symbols []Symbol) (generation uint64, err error) {
	file, fileData, symbolData, symbols, err := s.prepareSource(file, symbols)
	if err != nil {
		return 0, err
	}
	ctx, cancel := s.context(ctx)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, storeError("begin source update", err)
	}
	defer rollbackTransaction(tx, &err)
	if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO roots(root) VALUES (?)", file.RootID); err != nil {
		return 0, storeError("initialize root", err)
	}
	if err = tx.QueryRowContext(ctx, "SELECT generation FROM roots WHERE root=?", file.RootID).Scan(&generation); err != nil {
		return 0, storeError("read generation", err)
	}
	if generation >= math.MaxInt64 {
		return 0, ErrCapacity
	}
	generation++
	var oldData []byte
	err = tx.QueryRowContext(ctx, `SELECT f.data FROM heads h JOIN files f ON f.root=h.root AND f.id=h.id AND f.version=h.version WHERE h.root=? AND h.id=?`, file.RootID, file.ID).Scan(&oldData)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, storeError("read current source", err)
	}
	var old File
	if len(oldData) > 0 {
		if err = json.Unmarshal(oldData, &old); err != nil {
			return 0, fmt.Errorf("index: decode current source: %w", err)
		}
	}
	for _, query := range []string{
		"UPDATE files INDEXED BY files_current SET until_version=? WHERE root=? AND id=? AND until_version=0",
		"UPDATE symbols INDEXED BY symbols_file SET until_version=? WHERE root=? AND file=? AND until_version=0",
	} {
		if _, err = tx.ExecContext(ctx, query, generation, file.RootID, file.ID); err != nil {
			return 0, storeError("retire previous source", err)
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO files(root,id,version,data) VALUES (?,?,?,?)", file.RootID, file.ID, generation, fileData); err != nil {
		return 0, storeError("write source", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO heads(root,id,version,path,parent) VALUES (?,?,?,?,?) ON CONFLICT(root,id) DO UPDATE SET version=excluded.version,path=excluded.path,parent=excluded.parent`, file.RootID, file.ID, generation, file.Path, parentPath(file.Path)); err != nil {
		return 0, storeError("replace source head", err)
	}
	for i, symbol := range symbols {
		if _, err = tx.ExecContext(ctx, `INSERT INTO symbols(root,id,version,file,path,name,start_byte,kind,data,size) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			file.RootID, symbol.ID, generation, file.ID, file.Path, []byte(symbol.Name), symbol.StartByte, symbol.Kind, symbolData[i], len(symbolData[i])); err != nil {
			return 0, storeError("write declaration", err)
		}
	}
	newFile := fileCount(file) - fileCount(old)
	if _, err = tx.ExecContext(ctx, `UPDATE roots SET generation=?,files=files+?,pending=pending+?,indexed=indexed+?,omitted=omitted+? WHERE root=?`,
		generation, newFile, stateCount(file, StatePending)-stateCount(old, StatePending),
		stateCount(file, StateIndexed)-stateCount(old, StateIndexed),
		stateCount(file, StateOmitted)-stateCount(old, StateOmitted), file.RootID); err != nil {
		return 0, storeError("update root counters", err)
	}
	return generation, commitTransaction(tx)
}

func fileCount(file File) int64 {
	if file.Kind == KindFile {
		return 1
	}
	return 0
}

func stateCount(file File, target SourceState) int64 {
	if file.Kind == KindFile && file.State == target {
		return 1
	}
	return 0
}

func rollbackTransaction(tx *sql.Tx, err *error) {
	if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
		*err = errors.Join(*err, storeError("rollback transaction", rollbackErr))
	}
}

func commitTransaction(tx *sql.Tx) error {
	if err := tx.Commit(); err != nil {
		return storeError("commit transaction", err)
	}
	return nil
}

// EntryLookup returns the latest raw path mapping and observed metadata.
func (s *Store) EntryLookup(ctx context.Context, rootID, fileID string) (File, error) {
	ctx, cancel := s.context(ctx)
	defer cancel()
	var data []byte
	var size int64
	err := s.db.QueryRowContext(ctx, `SELECT length(f.data),substr(f.data,1,?) FROM heads h JOIN files f ON f.root=h.root AND f.id=h.id AND f.version=h.version WHERE h.root=? AND h.id=?`, s.options.MaxRecordBytes, rootID, fileID).Scan(&size, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return File{}, ErrNotFound
	}
	if err != nil {
		return File{}, storeError("lookup source", err)
	}
	if size > int64(s.options.MaxRecordBytes) {
		return File{}, ErrTooLarge
	}
	var file File
	if err := json.Unmarshal(data, &file); err != nil {
		return File{}, fmt.Errorf("index: decode source: %w", err)
	}
	return file, nil
}

// Status reads constant-size counters rather than counting the source table.
func (s *Store) Status(ctx context.Context, rootID string) (Status, error) {
	ctx, cancel := s.context(ctx)
	defer cancel()
	var status Status
	err := s.db.QueryRowContext(ctx, "SELECT generation,files,pending,indexed,omitted FROM roots WHERE root=?", rootID).
		Scan(&status.Generation, &status.Files, &status.Pending, &status.Indexed, &status.Omitted)
	if errors.Is(err, sql.ErrNoRows) {
		return Status{}, nil
	}
	if err != nil {
		return Status{}, storeError("read counters", err)
	}
	return status, nil
}
