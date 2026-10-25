package store

// rowShape is what the causal machinery needs to know about a table.
//
// classify, writeFields and the create path were written for tasks and are, it
// turns out, table-agnostic: all they ever needed was a table name and a way to
// turn a logical field into a column. Naming those two facts here is what lets
// lists share the arbitration instead of re-implementing it — and a second
// implementation of first-writer-wins is a second chance to get it wrong.
//
// The rest of this struct is the handful of columns that exist on one table and
// not another, stated once here rather than discovered as a driver error three
// layers down.
type rowShape struct {
	entity string
	table  string

	// since is the protocol version that put this entity in the vocabulary.
	// One line gates every entity by the version that introduced it.
	since int

	// column maps a logical field to the column it lands in. Unknown names are
	// not an error: they are kept in ext, which is half of what makes N-2
	// compatibility free.
	column func(field string) (column, bool)

	// mandatory is the single field a creation cannot do without. Exactly one,
	// for every entity: the product promise is that capturing something costs
	// one line of typing.
	mandatory string

	// kinds are the values a kind column accepts, the first being the default.
	// nil where the table has no such column. A kind is set once, at creation:
	// a task does not become an event by a field write.
	kinds []string

	// parent is the column naming the row this one hangs off, and parentIn the
	// table it points at. A create whose parent is gone is refused with the
	// author text kept, rather than left to abort on a foreign key.
	parent   string
	parentIn string

	// series is true for the one table whose rows can be occurrences of a
	// recurring chore; closes, for the one with a closed_rev column. tickable
	// says a completion may be recorded against the row at all.
	series   bool
	closes   bool
	tickable bool
}

// shapeOf is the whole list of tables the operation vocabulary can write.
//
// A switch and not a map, so there is no mutable global and the set of writable
// tables cannot be extended at runtime.
func shapeOf(entity string) (rowShape, bool) {
	switch entity {
	case EntityTask:
		return rowShape{
			entity: EntityTask, table: "tasks", since: 1,
			column: taskColumn, mandatory: "title",
			kinds:  []string{"task", "event"},
			series: true, closes: true, tickable: true,
		}, true

	case EntityList:
		return rowShape{
			entity: EntityList, table: "lists", since: protocolLists,
			column: listColumn, mandatory: "name",
			// One kind, deliberately. The spec asks for "a shared checklist:
			// groceries, a suitcase, things to buy" — which are NAMES people
			// give a list, not behaviours a client has to implement. Inventing
			// kinds now would be inventing product.
			kinds: []string{"checklist"},
		}, true

	case EntityListItem:
		return rowShape{
			entity: EntityListItem, table: "list_items", since: protocolLists,
			column: listItemColumn, mandatory: "label",
			parent: "list_id", parentIn: "lists",
			// An item is ticked by the same operation as a chore, and the tick
			// lands in the same table: completions.row_id is polymorphic on
			// purpose. So "who took the milk" is answered for free, and a
			// shared list in a supermarket shows two trolleys at once.
			tickable: true,
		}, true

	default:
		return rowShape{}, false
	}
}

// defaultKind is the kind a creation gets when it names none.
func (r rowShape) defaultKind() string {
	if len(r.kinds) == 0 {
		return ""
	}
	return r.kinds[0]
}

// accepts reports whether a kind is one this table allows.
func (r rowShape) accepts(kind string) bool {
	for _, allowed := range r.kinds {
		if kind == allowed {
			return true
		}
	}
	return false
}

// listColumn maps the writable fields of a list.
func listColumn(field string) (column, bool) {
	switch field {
	case "name":
		return column{name: "name", cast: "text", limit: 200}, true
	default:
		return column{}, false
	}
}

// listItemColumn maps the writable fields of an item.
//
// order_key is an ordinary causal field, and its being a STRING is the whole
// point. Two people reordering the same list on the underground write different
// fields of different rows, so both their moves land; the one case where they
// moved the same item resolves by first-writer-wins like any other field. A
// position held as an integer would renumber the neighbours on every move, so
// every move would collide with every other one.
func listItemColumn(field string) (column, bool) {
	switch field {
	case "label":
		return column{name: "label", cast: "text", limit: 500}, true
	case "qty":
		return column{name: "qty", cast: "numeric"}, true
	case "unit":
		return column{name: "unit", cast: "text"}, true
	case "order_key":
		// No bound, because the column carries none. A limit here and not in
		// the schema would make this file the authority for one column and the
		// database the authority for its neighbours — and the day they
		// disagree, the one that aborts the transaction wins.
		return column{name: "order_key", cast: "text"}, true
	default:
		// list_id is absent: an item does not move to another list by a field
		// write. It is set once, at creation.
		return column{}, false
	}
}
