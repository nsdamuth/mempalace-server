package storage

import (
	"fmt"
	"strings"
)

// qb (queryBuilder) allocates numbered PostgreSQL parameters ($1, $2, …).
// It is pre-seeded with the parameters that precede the WHERE clause (e.g. a
// query vector at $1), so WHERE conditions start at the correct offset.
type qb struct {
	args []any
	idx  int
}

func newQB(preArgs ...any) *qb {
	return &qb{args: preArgs, idx: len(preArgs)}
}

func (q *qb) next(val any) string {
	q.idx++
	q.args = append(q.args, val)
	return fmt.Sprintf("$%d", q.idx)
}

// WhereToSQL translates a ChromaDB-style filter map to a PostgreSQL WHERE
// fragment.  Parameters are allocated using qb.  Returns "TRUE" with no
// additional args when where is nil or empty.
//
// Supported operators: $eq $ne $in $nin $gt $gte $lt $lte $and $or.
// Leaf filters use the JSONB ->> operator (always TEXT); numeric comparisons
// cast to float via ::float.
func WhereToSQL(where map[string]any, q *qb) (string, error) {
	if len(where) == 0 {
		return "TRUE", nil
	}

	if subs, ok := where["$and"]; ok {
		return logicalOp("AND", subs, q)
	}
	if subs, ok := where["$or"]; ok {
		return logicalOp("OR", subs, q)
	}

	if len(where) != 1 {
		return "", fmt.Errorf("where dict must have exactly one key, got %d", len(where))
	}

	var field string
	var cond any
	for k, v := range where {
		field, cond = k, v
	}

	if !isValidField(field) {
		return "", fmt.Errorf("invalid metadata field name: %q", field)
	}

	condMap, isMap := cond.(map[string]any)
	if isMap {
		return operatorExpr(field, condMap, q)
	}

	// Simple equality: {"field": value}
	fp := q.next(field)
	vp := q.next(jsonbText(cond))
	return fmt.Sprintf("(metadata ->> %s) = %s", fp, vp), nil
}

func logicalOp(op string, raw any, q *qb) (string, error) {
	subs, err := toMapSlice(raw)
	if err != nil {
		return "", fmt.Errorf("$%s: %w", strings.ToLower(op), err)
	}
	parts := make([]string, 0, len(subs))
	for _, sub := range subs {
		part, err := WhereToSQL(sub, q)
		if err != nil {
			return "", err
		}
		parts = append(parts, "("+part+")")
	}
	return strings.Join(parts, " "+op+" "), nil
}

func operatorExpr(field string, cond map[string]any, q *qb) (string, error) {
	if len(cond) != 1 {
		return "", fmt.Errorf("condition must have one operator, got %d", len(cond))
	}
	var op string
	var val any
	for k, v := range cond {
		op, val = k, v
	}

	switch op {
	case "$eq":
		return fmt.Sprintf("(metadata ->> %s) = %s", q.next(field), q.next(jsonbText(val))), nil
	case "$ne":
		return fmt.Sprintf("(metadata ->> %s) != %s", q.next(field), q.next(jsonbText(val))), nil

	case "$in":
		vals, err := toAnySlice(val)
		if err != nil {
			return "", fmt.Errorf("$in: %w", err)
		}
		if len(vals) == 0 {
			return "FALSE", nil
		}
		fp := q.next(field)
		ph := make([]string, len(vals))
		for i, v := range vals {
			ph[i] = q.next(jsonbText(v))
		}
		return fmt.Sprintf("(metadata ->> %s) = ANY(ARRAY[%s]::text[])", fp, strings.Join(ph, ",")), nil

	case "$nin":
		vals, err := toAnySlice(val)
		if err != nil {
			return "", fmt.Errorf("$nin: %w", err)
		}
		if len(vals) == 0 {
			return "TRUE", nil
		}
		fp := q.next(field)
		ph := make([]string, len(vals))
		for i, v := range vals {
			ph[i] = q.next(jsonbText(v))
		}
		return fmt.Sprintf("NOT (metadata ->> %s) = ANY(ARRAY[%s]::text[])", fp, strings.Join(ph, ",")), nil

	case "$gt", "$gte", "$lt", "$lte":
		sqlOp := map[string]string{"$gt": ">", "$gte": ">=", "$lt": "<", "$lte": "<="}[op]
		f, err := toFloat(val)
		if err != nil {
			return "", fmt.Errorf("%s value: %w", op, err)
		}
		return fmt.Sprintf("(metadata ->> %s)::float %s %s", q.next(field), sqlOp, q.next(f)), nil
	}

	return "", fmt.Errorf("unsupported operator: %q", op)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// jsonbText converts a Go value to the string that PostgreSQL's ->> operator
// returns when extracting from JSONB.
func jsonbText(v any) string {
	switch t := v.(type) {
	case bool:
		if t {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprintf("%d", t)
	case int64:
		return fmt.Sprintf("%d", t)
	case float64:
		return fmt.Sprintf("%g", t)
	case float32:
		return fmt.Sprintf("%g", t)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func isValidField(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' ||
			(i > 0 && c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

func toMapSlice(v any) ([]map[string]any, error) {
	raw, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("expected array, got %T", v)
	}
	out := make([]map[string]any, len(raw))
	for i, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("element %d: expected object, got %T", i, r)
		}
		out[i] = m
	}
	return out, nil
}

func toAnySlice(v any) ([]any, error) {
	raw, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("expected array, got %T", v)
	}
	return raw, nil
}

func toFloat(v any) (float64, error) {
	switch t := v.(type) {
	case float64:
		return t, nil
	case float32:
		return float64(t), nil
	case int:
		return float64(t), nil
	case int64:
		return float64(t), nil
	}
	return 0, fmt.Errorf("cannot convert %T to float64", v)
}
