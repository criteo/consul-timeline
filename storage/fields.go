package storage

import (
	"strconv"

	tl "github.com/criteo/consul-timeline/timeline"
)

// FieldValue returns the value an event has for a filter field, in the
// form filters and facets use (enum names, not numbers).
func FieldValue(e tl.Event, field string) string {
	switch field {
	case FieldDatacenter:
		return e.Datacenter
	case FieldService:
		return e.ServiceName
	case FieldNode:
		return e.NodeName
	case FieldCheck:
		return e.CheckName
	case FieldTeam:
		return e.Team
	case FieldApp:
		return e.App
	case FieldVersion:
		return e.Version
	case FieldCheckType:
		return e.CheckType
	case FieldKind:
		return e.Kind.String()
	case FieldTo:
		return e.NewStatus().String()
	case FieldFrom:
		return e.OldStatus().String()
	case FieldHealthy:
		return strconv.Itoa(e.NewHealthy)
	}
	return ""
}

// FieldValues returns every value an event has for a field: each of its
// tags for FieldTag, at most one value otherwise.
func FieldValues(e tl.Event, field string) []string {
	if field == FieldTag {
		return e.Tags
	}
	if v := FieldValue(e, field); v != "" {
		return []string{v}
	}
	return nil
}

// FacetFields are the fields facets can be computed on.
var FacetFields = []string{FieldDatacenter, FieldKind, FieldTo, FieldTag, FieldTeam, FieldApp, FieldService, FieldNode, FieldCheck, FieldCheckType, FieldVersion}
