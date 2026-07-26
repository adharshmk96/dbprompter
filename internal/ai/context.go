package ai

import (
	"fmt"
	"strings"
)

// Above this many tables we stop sending the whole schema and switch to
// FTS-ranked selection plus foreign-key neighbors.
const fullSchemaLimit = 40

// buildContext renders the schema (or a relevant subset) as compact text for
// the model, including user tags and notes.
func (s *Service) buildContext(connID int64, question string) (string, error) {
	all, err := s.st.ListTables(connID, "")
	if err != nil {
		return "", err
	}
	if len(all) == 0 {
		return "", fmt.Errorf("no indexed tables for this connection — run indexing first")
	}

	include := map[int64]bool{}
	if len(all) <= fullSchemaLimit {
		for _, t := range all {
			include[t.ID] = true
		}
	} else {
		ids, err := s.st.SearchTableIDs(connID, question, 15)
		if err != nil {
			return "", err
		}
		for _, id := range ids {
			include[id] = true
		}
		// fall back to the first N tables if the question matched nothing
		if len(include) == 0 {
			for i, t := range all {
				if i >= fullSchemaLimit {
					break
				}
				include[t.ID] = true
			}
		}
		// expand with FK neighbors so joins stay possible
		byName := map[string]int64{}
		for _, t := range all {
			byName[t.Name] = t.ID
		}
		fks, err := s.st.AllFKs(connID)
		if err != nil {
			return "", err
		}
		neighbors := map[int64]bool{}
		for _, fk := range fks {
			fromID, toID := byName[fk.FromTable], byName[fk.ToTable]
			if include[fromID] && toID != 0 {
				neighbors[toID] = true
			}
			if include[toID] && fromID != 0 {
				neighbors[fromID] = true
			}
		}
		for id := range neighbors {
			include[id] = true
		}
	}

	var b strings.Builder
	includedNames := map[string]bool{}
	for _, t := range all {
		if !include[t.ID] {
			continue
		}
		includedNames[t.Name] = true
		detail, err := s.st.GetTable(t.ID)
		if err != nil {
			return "", err
		}
		qualified := t.Name
		if t.Schema != "" && t.Schema != "public" {
			qualified = t.Schema + "." + t.Name
		}
		fmt.Fprintf(&b, "TABLE %s", qualified)
		var meta []string
		if t.Comment != "" {
			meta = append(meta, t.Comment)
		}
		if t.Tags != "" {
			meta = append(meta, "tags: "+t.Tags)
		}
		if t.Note != "" {
			meta = append(meta, t.Note)
		}
		if t.RowCount >= 0 {
			meta = append(meta, fmt.Sprintf("~%d rows", t.RowCount))
		}
		if len(meta) > 0 {
			fmt.Fprintf(&b, "  -- %s", strings.Join(meta, "; "))
		}
		b.WriteString("\n")
		for _, c := range detail.Columns {
			fmt.Fprintf(&b, "  %s %s", c.Name, c.DataType)
			if c.PK {
				b.WriteString(" PRIMARY KEY")
			}
			if !c.Nullable {
				b.WriteString(" NOT NULL")
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	fks, err := s.st.AllFKs(connID)
	if err != nil {
		return "", err
	}
	var fkLines []string
	for _, fk := range fks {
		if includedNames[fk.FromTable] && includedNames[fk.ToTable] {
			fkLines = append(fkLines, fmt.Sprintf("  %s.%s -> %s.%s", fk.FromTable, fk.FromColumn, fk.ToTable, fk.ToColumn))
		}
	}
	if len(fkLines) > 0 {
		b.WriteString("FOREIGN KEYS:\n")
		b.WriteString(strings.Join(fkLines, "\n"))
		b.WriteString("\n")
	}
	return b.String(), nil
}
