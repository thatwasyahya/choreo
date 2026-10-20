package httpapi_test

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/thatwasyahya/choreo/server/internal/store"
)

// The seven cases of docs/SYNC_PROTOCOL.md, driven by two simulated clients
// against a real PostgreSQL.
//
// This is the exit criterion of Phase 1, and the reason it is written as seven
// named tests rather than as a pile of assertions: these are the situations a
// household will actually meet, and each one has a promised behaviour. A test
// per case fails with the name of the promise it broke.
//
// Every test here goes through HTTP and through the database. Nothing is
// stubbed: a stub would only prove the stub agrees with itself.

// --- 1 ----------------------------------------------------------------------

// Two adults tick the same chore, both offline.
//
// There is no conflict, because a completion is not a flag: it is a set indexed
// by member. The specification names this as the unforgivable case, and it is
// answered by HAVING no conflict rather than by resolving one.
func TestCaseOneBothTickTheSameChoreOffline(t *testing.T) {
	a := newTestAPI(t)

	camille := a.newPhone("Camille", "camille")
	camille.found("Maison")
	jordan := a.newPhone("Jordan", "jordan")
	jordan.joins(camille.invites("adult"))

	chore := camille.creates("Sortir les poubelles")
	camille.sync()
	jordan.pull()

	// Both in a cellar, neither can see the other.
	camilleTick := camille.ticks(chore)
	jordanTick := jordan.ticks(chore)

	// Camille surfaces first.
	if outcome := str(only(t, camille.sync()), "outcome"); outcome != "applied" {
		t.Errorf("Camille's tick: outcome = %q, want applied", outcome)
	}
	// Then Jordan, who had seen nothing of it.
	if outcome := str(only(t, jordan.sync()), "outcome"); outcome != "applied" {
		t.Errorf("Jordan's tick: outcome = %q, want applied", outcome)
	}

	camille.pull()

	for _, p := range []*phone{camille, jordan} {
		done := p.doneBy(chore)
		if len(done) != 2 {
			t.Errorf("%s sees %d completions, want both: %v", p.name, len(done), done)
		}
		if !slices.Contains(done, camille.member) || !slices.Contains(done, jordan.member) {
			t.Errorf("%s sees %v, want both members", p.name, done)
		}

		// Nothing to look at, nothing to decide: no card on either side.
		if cards := p.waiting(); len(cards) != 0 {
			t.Errorf("%s was handed %d cards for a case with no conflict: %v",
				p.name, len(cards), cards)
		}
	}

	// Both completion ids survive: they are the deduplication key for points
	// (Phase 3), so neither tick may be quietly folded into the other.
	ids := camille.completionIDs(chore)
	if !slices.Contains(ids, camilleTick) || !slices.Contains(ids, jordanTick) {
		t.Errorf("completion ids = %v, want both %s and %s", ids, camilleTick, jordanTick)
	}
}

// The same person ticking on two devices is ONE completion, so one point. The
// completion is indexed (row, member), never (row, device).
func TestCaseOneTheSamePersonOnTwoDevicesTicksOnce(t *testing.T) {
	a := newTestAPI(t)

	camille := a.newPhone("Camille", "camille")
	camille.found("Maison")
	tablet := camille.tablet("camille")

	chore := camille.creates("Vider le lave-vaisselle")
	camille.sync()
	tablet.pull()

	camille.ticks(chore)
	tablet.ticks(chore)

	camille.sync()
	second := only(t, tablet.sync())

	if outcome := str(second, "outcome"); outcome != "absorbed" {
		t.Errorf("the second device's tick: outcome = %q, want absorbed", outcome)
	}

	camille.pull()
	if done := camille.doneBy(chore); len(done) != 1 {
		t.Errorf("completions = %v, want exactly one: never \"ticked by Camille and Camille\"",
			done)
	}
}

// --- 2 ----------------------------------------------------------------------

// One renames the task, the other deletes it — the deletion arriving first.
//
// The field is NOT written: writing under a tombstone serves nobody. The editor
// gets a card carrying their own text, and the content columns are kept so that
// restoring shows them what they are getting back.
func TestCaseTwoADeletionFirstThenARename(t *testing.T) {
	a := newTestAPI(t)

	camille := a.newPhone("Camille", "camille")
	camille.found("Maison")
	jordan := a.newPhone("Jordan", "jordan")
	jordan.joins(camille.invites("adult"))

	chore := camille.creates("Appeler le plombier")
	camille.sync()
	jordan.pull()

	// Both offline. Jordan is editing the title; Camille is throwing it away.
	jordan.renames(chore, "Appeler le plombier (devis)")
	camille.deletes(chore)

	camille.sync()
	rename := only(t, jordan.sync())

	if outcome := str(rename, "outcome"); outcome != "receipted" {
		t.Errorf("the rename: outcome = %q, want receipted", outcome)
	}
	if code := str(rename, "code"); code != "edit_vs_delete" {
		t.Errorf("the rename: code = %q, want edit_vs_delete", code)
	}

	jordan.pull()

	if !jordan.buried(chore) {
		t.Error("the deletion did not hold the display: deletion wins, and restoring is one tap")
	}
	if title := jordan.title(chore); title != "Appeler le plombier" {
		t.Errorf("title = %q, want the original kept under the tombstone", title)
	}

	// The card goes to the editor, and to nobody else: the deleter already has
	// what they asked for, and two people answering is a race.
	cards := jordan.waiting()
	if len(cards) != 1 {
		t.Fatalf("Jordan holds %d cards, want exactly one: %v", len(cards), cards)
	}
	if code := str(cards[0], "code"); code != "edit_vs_delete" {
		t.Errorf("the card's code = %q, want edit_vs_delete", code)
	}

	mine, _ := cards[0]["mine"].(map[string]any)
	if str(mine, "title") != "Appeler le plombier (devis)" {
		t.Errorf("the card carries %v, want Jordan's own text verbatim", mine)
	}

	camille.pull()
	if cards := camille.waiting(); len(cards) != 0 {
		t.Errorf("the deleter was handed %d cards, want none", len(cards))
	}

	// Restoring is an ordinary operation, and it brings the content back with
	// the title the editor wanted.
	jordan.restores(chore)
	jordan.renames(chore, "Appeler le plombier (devis)")
	jordan.sync()
	jordan.pull()

	if jordan.buried(chore) {
		t.Error("restoring did not raise the row")
	}
	if title := jordan.title(chore); title != "Appeler le plombier (devis)" {
		t.Errorf("title after restoring = %q, want the editor's text", title)
	}
}

// The symmetric half: the edit arrives first, so the deletion is stale. The
// editor's work stands and the DELETER is the one told about it, because a
// deletion destroys — it asks, it does not impose.
func TestCaseTwoBARenameFirstThenAStaleDeletion(t *testing.T) {
	a := newTestAPI(t)

	camille := a.newPhone("Camille", "camille")
	camille.found("Maison")
	jordan := a.newPhone("Jordan", "jordan")
	jordan.joins(camille.invites("adult"))

	chore := camille.creates("Appeler le plombier")
	camille.sync()
	jordan.pull()

	jordan.renames(chore, "Appeler le plombier (devis)")
	camille.deletes(chore)

	jordan.sync()
	deletion := only(t, camille.sync())

	if outcome := str(deletion, "outcome"); outcome != "receipted" {
		t.Errorf("the stale deletion: outcome = %q, want receipted", outcome)
	}
	if code := str(deletion, "code"); code != "stale_delete" {
		t.Errorf("the stale deletion: code = %q, want stale_delete", code)
	}

	camille.pull()

	if camille.buried(chore) {
		t.Error("a stale deletion took the row down: the editor's work has to stand")
	}
	if title := camille.title(chore); title != "Appeler le plombier (devis)" {
		t.Errorf("title = %q, want the rename that arrived first", title)
	}

	cards := camille.waiting()
	if len(cards) != 1 {
		t.Fatalf("the deleter holds %d cards, want one: %v", len(cards), cards)
	}

	jordan.pull()
	if cards := jordan.waiting(); len(cards) != 0 {
		t.Errorf("the editor was handed %d cards, want none: their write landed", len(cards))
	}
}

// Answering a card is idempotent, and deliberately indistinguishable from
// answering one somebody else already answered: telling them apart would say
// whether another person still has an open card.
func TestCaseTwoAnsweringACardTwiceIsAbsorbed(t *testing.T) {
	a := newTestAPI(t)

	camille := a.newPhone("Camille", "camille")
	camille.found("Maison")
	jordan := a.newPhone("Jordan", "jordan")
	jordan.joins(camille.invites("adult"))

	chore := camille.creates("Appeler le plombier")
	camille.sync()
	jordan.pull()

	jordan.renames(chore, "Appeler le plombier (devis)")
	camille.deletes(chore)
	camille.sync()
	jordan.sync()
	jordan.pull()

	card := jordan.waiting()[0]
	noticeID := str(card, "id")

	jordan.answers(noticeID, "mine")
	if outcome := str(only(t, jordan.sync()), "outcome"); outcome != "applied" {
		t.Errorf("answering a card: outcome = %q, want applied", outcome)
	}

	jordan.answers(noticeID, "theirs")
	if outcome := str(only(t, jordan.sync()), "outcome"); outcome != "absorbed" {
		t.Errorf("answering it again: outcome = %q, want absorbed", outcome)
	}

	jordan.pull()
	if cards := jordan.waiting(); len(cards) != 0 {
		t.Errorf("the card is still open after being answered: %v", cards)
	}
}

// --- 3 ----------------------------------------------------------------------

// A series, two phones offline, exactly one open occurrence.
//
// Both tick occurrence #0. Both completions are recorded — neither person loses
// the credit — but only one advance happens, because the advance is a
// compare-and-swap on the register. Zero rows updated is NOT an error: a
// human's tick is never destroyed by a constraint failure.
func TestCaseThreeASeriesAdvancesExactlyOnce(t *testing.T) {
	a := newTestAPI(t)

	camille := a.newPhone("Camille", "camille")
	camille.found("Maison")
	jordan := a.newPhone("Jordan", "jordan")
	jordan.joins(camille.invites("adult"))

	seriesID := uuid.New()
	first := store.OccurrenceID(seriesID, 0)
	next := store.OccurrenceID(seriesID, 1)

	// A series is seeded with SQL: creating one is a Phase 2 operation, and the
	// invariant under test is already here.
	// next_index holds the index of the occurrence currently open, so advancing
	// increments it to the one being created.
	execSQLFor(t, `INSERT INTO series (id, household_id, rev, created_rev, updated_by,
	                                   title, mode, rule, anchor_on, next_index)
	               VALUES ($1, $2, 1, 1, $3, $4, 'calendar', 'FREQ=WEEKLY',
	                       current_date, 0)`,
		seriesID, uuid.MustParse(camille.house), uuid.MustParse(camille.member), "Poubelles")
	execSQLFor(t, `INSERT INTO tasks (id, household_id, rev, created_rev, updated_by,
	                                  kind, title, series_id, series_index)
	               VALUES ($1, $2, 2, 2, $3, 'task', $4, $5, 0)`,
		first, uuid.MustParse(camille.house), uuid.MustParse(camille.member),
		"Poubelles", seriesID)
	execSQLFor(t, `UPDATE series SET open_occurrence_id = $3
	                WHERE household_id = $1 AND id = $2`,
		uuid.MustParse(camille.house), seriesID, first)

	camille.pull()
	jordan.pull()

	// Camille in a cellar, Jordan in the underground. Both tick the same #0,
	// and both phones predict the same #1 — the identifier derives from an
	// integer, never from a date, so no tzdata skew can make them differ.
	camille.ticks(first.String())
	jordan.ticks(first.String())

	camilleResult := only(t, camille.sync())
	jordanResult := only(t, jordan.sync())

	if outcome := str(camilleResult, "outcome"); outcome != "applied" {
		t.Errorf("Camille's tick: outcome = %q, want applied", outcome)
	}
	if outcome := str(jordanResult, "outcome"); outcome != "applied" {
		t.Errorf("Jordan's tick: outcome = %q, want applied — zero rows advanced is not an error",
			outcome)
	}

	advanced := map[string]string{}
	for _, result := range []map[string]any{camilleResult, jordanResult} {
		fields, _ := result["fields"].(map[string]any)
		advanced[str(result, "op_id")] = str(fields, "series_advanced")
	}

	yes, no := 0, 0
	for _, value := range advanced {
		switch value {
		case "true":
			yes++
		case "false":
			no++
		}
	}
	if yes != 1 || no != 1 {
		t.Errorf("series_advanced = %v, want exactly one advance and one no-op", advanced)
	}

	// The invariant itself, read from the database: one open occurrence, and it
	// is the #1 both phones had predicted.
	var open uuid.UUID
	execScalar(t, &open, `SELECT open_occurrence_id FROM series
	                       WHERE household_id = $1 AND id = $2`,
		uuid.MustParse(camille.house), seriesID)
	if open != next {
		t.Errorf("open occurrence = %s, want the predicted %s", open, next)
	}

	var occurrences int
	execScalar(t, &occurrences, `SELECT count(*) FROM tasks
	                              WHERE household_id = $1 AND series_id = $2`,
		uuid.MustParse(camille.house), seriesID)
	if occurrences != 2 {
		t.Errorf("%d occurrences exist, want two: #0 done and #1 open", occurrences)
	}

	// Both phones converge, and both keep their credit on #0.
	camille.pull()
	jordan.pull()
	for _, p := range []*phone{camille, jordan} {
		if done := p.doneBy(first.String()); len(done) != 2 {
			t.Errorf("%s sees %v on the ticked occurrence, want both members", p.name, done)
		}
		if p.rows[next.String()].rev == 0 {
			t.Errorf("%s never received the next occurrence", p.name)
		}
	}
}

// --- 4 ----------------------------------------------------------------------

// The shopping list, both phones in the aisle.
//
// No new mechanism, which is the point. Two people adding "lait" get two rows,
// because a list is not a set of strings and merging would hide one person's
// intention. A tick survives an untick that never saw it — tick-wins — and an
// untick only removes what it observed, which closes the hole a stale untick
// would otherwise walk through four days later.
//
// Driven on tasks: list_item is a Phase 2 entity, and the mechanism under test
// is the one the specification says lists share with tasks.
func TestCaseFourTwoPeopleInTheAisle(t *testing.T) {
	a := newTestAPI(t)

	camille := a.newPhone("Camille", "camille")
	camille.found("Maison")
	jordan := a.newPhone("Jordan", "jordan")
	jordan.joins(camille.invites("adult"))

	// Both add milk, neither having seen the other.
	camilleMilk := camille.creates("Lait")
	jordanMilk := jordan.creates("Lait")
	camille.sync()
	jordan.sync()
	camille.pull()

	if camilleMilk == jordanMilk {
		t.Fatal("the two phones minted the same row id")
	}
	for _, rowID := range []string{camilleMilk, jordanMilk} {
		if camille.buried(rowID) || camille.title(rowID) != "Lait" {
			t.Errorf("row %s did not survive as its own line", rowID)
		}
	}

	// Camille ticks the milk off and both phones see it.
	camille.ticks(jordanMilk)
	camille.sync()
	jordan.pull()

	// Now they part. Camille changes her mind and unticks — against the one
	// completion she can see, her own. Jordan, in the next aisle, ticks it too.
	camille.unticks(jordanMilk)
	jordan.ticks(jordanMilk)

	jordan.sync()
	untick := only(t, camille.sync())

	if outcome := str(untick, "outcome"); outcome != "applied" &&
		outcome != "absorbed" && outcome != "superseded" {
		t.Errorf("the untick: outcome = %q, want a silent one", outcome)
	}

	camille.pull()
	done := camille.doneBy(jordanMilk)
	if len(done) != 1 || done[0] != jordan.member {
		t.Errorf("completions = %v, want Jordan's tick alone: an untick removes only what "+
			"it observed, so the tick it never saw survives — and the article stays "+
			"ticked, which is the right default in an aisle", done)
	}
}

// --- 5 ----------------------------------------------------------------------

// A phone comes back after ten days.
//
// It gets the CURRENT STATE of the rows that changed, not ten days of
// operations. Then it pushes what it queued, and the receipt comes back in the
// same round trip.
func TestCaseFiveAPhoneComesBackAfterTenDays(t *testing.T) {
	a := newTestAPI(t)

	camille := a.newPhone("Camille", "camille")
	camille.found("Maison")
	jordan := a.newPhone("Jordan", "jordan")
	jordan.joins(camille.invites("adult"))

	chore := camille.creates("Appeler le plombier")
	camille.sync()
	jordan.pull()

	// Ten days of a busy household, all of it while Jordan is away.
	for _, title := range []string{"Courses", "Pharmacie", "Vidange", "Cantine"} {
		camille.creates(title)
	}
	camille.renames(chore, "Appeler le plombier (urgent)")
	camille.sync()

	// Jordan, offline all along, has been working too.
	jordansOwn := jordan.creates("Réserver le train")
	jordan.ticks(chore)

	before := jordan.cursor
	jordan.sync()

	if jordan.cursor <= before {
		t.Error("the cursor did not move: a phone that cannot advance comes back forever")
	}
	if title := jordan.title(chore); title != "Appeler le plombier (urgent)" {
		t.Errorf("title = %q, want the state as it is now, not ten days of operations", title)
	}
	if len(jordan.rows) < 6 {
		t.Errorf("Jordan holds %d rows, want the whole household", len(jordan.rows))
	}
	if jordan.buried(jordansOwn) || jordan.title(jordansOwn) != "Réserver le train" {
		t.Error("Jordan's own creation did not come back in the same round trip")
	}
	if len(jordan.outbox) != 0 {
		t.Errorf("%d operations are still queued after a successful push", len(jordan.outbox))
	}

	camille.pull()
	if done := camille.doneBy(chore); len(done) != 1 {
		t.Errorf("completions = %v, want Jordan's tick from ten days of silence", done)
	}
}

// An expired cursor is NOT a 410 that discards what was just pushed. The
// operations are applied and answered, and the answer says a snapshot is
// needed — which is the whole reason it is a 200 and not an error.
func TestCaseFiveAnExpiredCursorKeepsTheOutbox(t *testing.T) {
	a := newTestAPI(t)

	camille := a.newPhone("Camille", "camille")
	camille.found("Maison")

	chore := camille.creates("Poubelles")
	camille.sync()

	// The household's history has been purged past this phone's position.
	execSQLFor(t, `UPDATE households SET purge_horizon_seq = change_seq + 1 WHERE id = $1`,
		uuid.MustParse(camille.house))

	camille.renames(chore, "Poubelles, ce soir")
	results := camille.sync()

	if outcome := str(only(t, results), "outcome"); outcome != "applied" {
		t.Errorf("the pushed rename: outcome = %q, want applied — an expired cursor must not "+
			"discard what the person just did", outcome)
	}
}

// --- 6 ----------------------------------------------------------------------

// A queued operation is refused.
//
// Five outcomes exist and only two reach the user. This case checks the two
// refusals that do: one a person can act on, and one that is our bug and stays
// between us.
func TestCaseSixARefusalThatAPersonCanActOn(t *testing.T) {
	a := newTestAPI(t)

	camille := a.newPhone("Camille", "camille")
	camille.found("Maison")

	// A tick on something that no longer exists anywhere: work somebody really
	// did, which the server cannot record. It must not vanish quietly.
	camille.ticks(uuid.NewString())
	refusal := only(t, camille.sync())

	if outcome := str(refusal, "outcome"); outcome != "refused" {
		t.Errorf("a tick on a missing row: outcome = %q, want refused", outcome)
	}
	if code := str(refusal, "code"); code != "target_gone" {
		t.Errorf("code = %q, want target_gone", code)
	}
	if refusal["notice_id"] == nil {
		t.Error("the refusal carries no card: the tick would disappear in silence")
	}

	camille.pull()
	if cards := camille.waiting(); len(cards) != 1 {
		t.Errorf("the author holds %d cards, want one for work that did not land", len(cards))
	}
}

// Malformed is our bug, not theirs. It is refused, logged, and earns NO card:
// the user is not our monitoring.
func TestCaseSixOurOwnBugDoesNotBecomeTheirCard(t *testing.T) {
	a := newTestAPI(t)

	camille := a.newPhone("Camille", "camille")
	camille.found("Maison")

	camille.queue(map[string]any{
		"kind": "create", "row_id": uuid.NewString(),
		"fields": map[string]any{"title": "   "},
	})
	refusal := only(t, camille.sync())

	if code := str(refusal, "code"); code != "malformed" {
		t.Errorf("code = %q, want malformed", code)
	}
	if refusal["notice_id"] != nil {
		t.Error("a malformed operation raised a card: that is our bug to fix, not theirs to read")
	}

	camille.pull()
	if cards := camille.waiting(); len(cards) != 0 {
		t.Errorf("%d cards raised for our own bug, want none", len(cards))
	}
}

// An operation this server is too old to understand is RETRYABLE, never
// terminal: with direct APK distribution that is ordinary, and a deploy-order
// mistake must not poison a real person's outbox forever.
func TestCaseSixAnUnsupportedOperationStaysInTheQueue(t *testing.T) {
	a := newTestAPI(t)

	camille := a.newPhone("Camille", "camille")
	camille.found("Maison")

	camille.queue(map[string]any{
		"kind": "nourish_the_cat", "row_id": uuid.NewString(),
		"fields": map[string]any{"title": "Nourrir le chat"},
	})
	refusal := only(t, camille.sync())

	if code := str(refusal, "code"); code != "unsupported_op" {
		t.Fatalf("code = %q, want unsupported_op", code)
	}
	if len(camille.outbox) != 1 {
		t.Error("a retryable refusal left the queue: a deploy fixes this, dropping it does not")
	}
}

// --- 7 ----------------------------------------------------------------------

// The same user on a phone and a tablet.
//
// It falls out with no special case, because the unit of synchronisation is the
// device and not the person. Two devices, two cursors, two outboxes, one member
// id — and `changes` is not filtered by device, so the tablet applies the
// phone's write by the same path as anybody else's. The shortcut "this one is
// mine, skip it" is the shortcut that ends in divergence.
func TestCaseSevenAPhoneAndATablet(t *testing.T) {
	a := newTestAPI(t)

	camille := a.newPhone("Camille", "camille")
	camille.found("Maison")
	tablet := camille.tablet("camille")

	if camille.deviceID == tablet.deviceID {
		t.Fatal("the two devices share an id: they would share a cursor")
	}
	if camille.member != tablet.member {
		t.Fatal("the two devices do not share a member id: the same person would score twice")
	}

	chore := camille.creates("Changer les draps")
	camille.sync()

	tablet.pull()
	if title := tablet.title(chore); title != "Changer les draps" {
		t.Errorf("the tablet sees %q: changes are not filtered by device", title)
	}

	// Edited on the tablet, seen on the phone.
	tablet.renames(chore, "Changer les draps du haut")
	tablet.sync()
	camille.pull()
	if title := camille.title(chore); title != "Changer les draps du haut" {
		t.Errorf("the phone sees %q, want the tablet's edit", title)
	}
}

// A card is addressed to a MEMBER and stored on the server, so answering it on
// the tablet closes it on the phone — and the refused content survives a phone
// that is lost or wiped, which is the hole every examined proposal shared.
func TestCaseSevenACardAnsweredOnOneDeviceClosesOnTheOther(t *testing.T) {
	a := newTestAPI(t)

	camille := a.newPhone("Camille", "camille")
	camille.found("Maison")
	jordan := a.newPhone("Jordan", "jordan")
	jordan.joins(camille.invites("adult"))

	chore := camille.creates("Appeler le plombier")
	camille.sync()
	jordan.pull()

	tablet := jordan.tablet("jordan")
	tablet.pull()

	jordan.renames(chore, "Appeler le plombier (devis)")
	camille.deletes(chore)
	camille.sync()
	jordan.sync()

	jordan.pull()
	tablet.pull()

	cards := tablet.waiting()
	if len(cards) != 1 {
		t.Fatalf("the tablet holds %d cards, want the one addressed to its member: %v",
			len(cards), cards)
	}
	mine, _ := cards[0]["mine"].(map[string]any)
	if str(mine, "title") != "Appeler le plombier (devis)" {
		t.Errorf("the tablet sees %v, want the text typed on the phone: the refused content "+
			"lives on the server, not on the device that can disappear", mine)
	}

	tablet.answers(str(cards[0], "id"), "theirs")
	tablet.sync()

	jordan.pull()
	if cards := jordan.waiting(); len(cards) != 0 {
		t.Errorf("the phone still holds %d cards after the tablet answered: %v",
			len(cards), cards)
	}
}

// Every operation in one batch gets its own verdict, and the batch is not
// all-or-nothing: one bad operation must not take the good ones down with it.
func TestABatchAnswersEveryOperationOnItsOwn(t *testing.T) {
	a := newTestAPI(t)

	camille := a.newPhone("Camille", "camille")
	camille.found("Maison")

	good := camille.creates("Courses")
	camille.queue(map[string]any{
		"kind": "create", "row_id": uuid.NewString(),
		"fields": map[string]any{"title": ""},
	})
	alsoGood := camille.creates("Pharmacie")

	verdicts := outcomes(camille.sync())
	if len(verdicts) != 3 {
		t.Fatalf("got %d verdicts for 3 operations: %v", len(verdicts), verdicts)
	}

	applied := 0
	for _, outcome := range verdicts {
		if outcome == "applied" {
			applied++
		}
	}
	if applied != 2 {
		t.Errorf("%d operations applied, want the two good ones: %v", applied, verdicts)
	}

	camille.pull()
	for _, rowID := range []string{good, alsoGood} {
		if camille.title(rowID) == "" {
			t.Errorf("row %s did not land although its own operation was fine", rowID)
		}
	}
}
