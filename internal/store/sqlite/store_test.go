package sqlite

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Csneer/agent-task-bridge/internal/domain"
	"github.com/Csneer/agent-task-bridge/internal/ports"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func begin(t *testing.T, store *Store, write bool) ports.Transaction {
	t.Helper()
	tx, err := store.Begin(context.Background(), write)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tx.Rollback)
	return tx
}

func putBaseTask(t *testing.T, store *Store) {
	t.Helper()
	tx := begin(t, store, true)
	if err := tx.PutTask(domain.Task{TaskID: "T", Revision: 1, State: "draft"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func record(kind, id, relation, relationID string) domain.Record {
	keys := map[string]string{"Assignment": "assignment_id", "Run": "run_id", "Submission": "submission_id", "Decision": "decision_id"}
	data := domain.Object{keys[kind]: id, "task_id": "T"}
	if relation != "" {
		data[relation] = relationID
	}
	return domain.Record{RecordType: kind, Record: data}
}

func TestWALSnapshotAndTransactionAtomicity(t *testing.T) {
	store := openTestStore(t)
	var mode string
	if err := store.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal mode %s: %v", mode, err)
	}
	reader := begin(t, store, false)
	if tasks, err := reader.Tasks(); err != nil || len(tasks) != 0 {
		t.Fatalf("initial snapshot %v: %v", tasks, err)
	}
	writer := begin(t, store, true)
	if err := writer.PutTask(domain.Task{TaskID: "T", Revision: 1, State: "draft"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := writer.AppendBatch(domain.CommitBatch{CommitID: "C"}, "T"); err != nil {
		t.Fatal(err)
	}
	if err := writer.PutReceipt(Receipt{Principal: "P", Key: "K", TaskID: "T", Payload: []byte("result")}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Commit(); err != nil {
		t.Fatal(err)
	}
	if tasks, err := reader.Tasks(); err != nil || len(tasks) != 0 {
		t.Fatalf("WAL snapshot mixed concurrent state %v: %v", tasks, err)
	}
	if watermark, err := reader.Watermark(); err != nil || watermark != 0 {
		t.Fatalf("snapshot watermark moved %d: %v", watermark, err)
	}
	reader.Rollback()
	newReader := begin(t, store, false)
	if task, err := newReader.Task("T"); err != nil || task == nil || task.Revision != 1 {
		t.Fatalf("committed task missing: %v %v", task, err)
	}
	if watermark, err := newReader.Watermark(); err != nil || watermark != 1 {
		t.Fatalf("committed watermark missing %d: %v", watermark, err)
	}
	if receipt, err := newReader.Receipt("P", "K"); err != nil || receipt == nil || string(receipt.Payload) != "result" {
		t.Fatalf("committed receipt missing: %v %v", receipt, err)
	}
}

func TestUniqueFactsAndCAS(t *testing.T) {
	store := openTestStore(t)
	putBaseTask(t, store)
	first := []domain.Record{
		record("Assignment", "A1", "state", "active"),
		record("Run", "R1", "assignment_id", "A1"),
		record("Submission", "S1", "run_id", "R1"),
		record("Decision", "D1", "submission_id", "S1"),
	}
	tx := begin(t, store, true)
	for _, fact := range first {
		if err := tx.PutRecord(fact); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	duplicates := []domain.Record{
		record("Assignment", "A2", "state", "claimed"),
		record("Run", "R2", "assignment_id", "A1"),
		record("Submission", "S2", "run_id", "R1"),
		record("Decision", "D2", "submission_id", "S1"),
	}
	for _, fact := range duplicates {
		tx := begin(t, store, true)
		if err := tx.PutRecord(fact); err == nil {
			t.Errorf("accepted duplicate unique %s", fact.RecordType)
		}
		tx.Rollback()
	}
	tx = begin(t, store, true)
	first[0].Record["state"] = "completed"
	if err := tx.PutRecord(first[0]); err != nil {
		t.Fatal(err)
	}
	if err := tx.PutRecord(record("Assignment", "A2", "state", "claimed")); err != nil {
		t.Fatal("new Assignment after old end rejected:", err)
	}
	if err := tx.PutRecord(record("Run", "R2", "assignment_id", "A2")); err != nil {
		t.Fatal(err)
	}
	if err := tx.PutRecord(record("Submission", "S2", "run_id", "R2")); err != nil {
		t.Fatal(err)
	}
	if err := tx.PutRecord(record("Decision", "D2", "submission_id", "S2")); err != nil {
		t.Fatal("second Submission could not have own Decision:", err)
	}
	if err := tx.PutTask(domain.Task{TaskID: "T", Revision: 2}, 1); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx = begin(t, store, true)
	if err := tx.PutTask(domain.Task{TaskID: "T", Revision: 3}, 1); err == nil {
		t.Fatal("stale Task CAS accepted")
	}
	tx.Rollback()
	reader := begin(t, store, false)
	current, err := reader.Task("T")
	if err != nil || current.Revision != 2 {
		t.Fatalf("failed CAS changed Task: %v %v", current, err)
	}
}

func TestReceiptsAreNamespacedAndFailedTransactionLeavesNoFacts(t *testing.T) {
	store := openTestStore(t)
	putBaseTask(t, store)
	tx := begin(t, store, true)
	if err := tx.PutReceipt(Receipt{Principal: "P", Key: "K", Hash: "original", TaskID: "T", Payload: []byte("original")}); err != nil {
		t.Fatal(err)
	}
	if err := tx.PutReceipt(Receipt{Principal: "Q", Key: "K", Hash: "other", TaskID: "T", Payload: []byte("other")}); err != nil {
		t.Fatal("different principal namespace rejected:", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx = begin(t, store, true)
	if err := tx.PutTask(domain.Task{TaskID: "T", Revision: 2}, 1); err != nil {
		t.Fatal(err)
	}
	if err := tx.AppendBatch(domain.CommitBatch{CommitID: "uncommitted"}, "T"); err != nil {
		t.Fatal(err)
	}
	if err := tx.PutReceipt(Receipt{Principal: "P", Key: "K", Hash: "replacement", TaskID: "T", Payload: []byte("replacement")}); err == nil {
		t.Fatal("existing receipt overwritten")
	}
	tx.Rollback()
	reader := begin(t, store, false)
	current, err := reader.Task("T")
	if err != nil || current.Revision != 1 {
		t.Fatalf("rolled-back task changed %v: %v", current, err)
	}
	if watermark, err := reader.Watermark(); err != nil || watermark != 0 {
		t.Fatalf("uncommitted event persisted %d: %v", watermark, err)
	}
	if receipt, err := reader.Receipt("P", "K"); err != nil || receipt.Hash != "original" {
		t.Fatalf("original receipt overwritten %v: %v", receipt, err)
	}
}

func TestProcessExitBeforeAndAfterCommit(t *testing.T) {
	for _, mode := range []string{"before", "after"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "crash.sqlite")
			command := exec.Command(os.Args[0], "-test.run=^TestCrashHelper$")
			command.Env = append(os.Environ(), "BRIDGE_ADAPTER_CRASH_DB="+path, "BRIDGE_ADAPTER_CRASH_MODE="+mode)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("crash helper failed: %v: %s", err, output)
			}
			store, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			tx := begin(t, store, false)
			task, err := tx.Task("T")
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := tx.Receipt("P", "K")
			if err != nil {
				t.Fatal(err)
			}
			watermark, err := tx.Watermark()
			if err != nil {
				t.Fatal(err)
			}
			committed := mode == "after"
			if (task != nil) != committed || (receipt != nil) != committed || (watermark == 1) != committed {
				t.Fatalf("crash split facts: task=%v receipt=%v watermark=%d", task != nil, receipt != nil, watermark)
			}
			tx.Rollback()
		})
	}
}

func TestCrashHelper(t *testing.T) {
	path := os.Getenv("BRIDGE_ADAPTER_CRASH_DB")
	if path == "" {
		return
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	tx := begin(t, store, true)
	if err := tx.PutTask(domain.Task{TaskID: "T", Revision: 1}, 0); err != nil {
		t.Fatal(err)
	}
	if err := tx.AppendBatch(domain.CommitBatch{CommitID: "C"}, "T"); err != nil {
		t.Fatal(err)
	}
	if err := tx.PutReceipt(Receipt{Principal: "P", Key: "K", TaskID: "T", Payload: []byte("success")}); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("BRIDGE_ADAPTER_CRASH_MODE") == "after" {
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	// Deliberately bypass Go cleanups and Store.Close, reproducing process loss.
	os.Exit(0)
}

func TestPersistentPathRequired(t *testing.T) {
	for _, path := range []string{"", ":memory:"} {
		if store, err := Open(path); err == nil {
			store.Close()
			t.Errorf("accepted nonpersistent path %q", path)
		}
	}
}
