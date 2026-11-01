package fundinglotcatalog

import "testing"

func TestCatalogComparisonTemplateTraversalPreservesFieldBoundary(t *testing.T) {
	names := map[string]string{"application": `"app-test"`}
	tmpl := object{"parts": []any{object{"schema": "application"}, object{"literal": `.safe(x)`}}}
	text := `"app-test".safe(x)`
	for _, field := range []string{"partition_bound", "partition_key", "default_expression", "argument_defaults", "expression", "expressions", "predicate", "when"} {
		if compareDescriptorValue(object{field: text}, object{field: tmpl}, names, "") != nil {
			t.Fatalf("reviewed deparse field %s rejected", field)
		}
	}
	actual := object{"relations": []any{object{"attributes": []any{object{"default": text}}}}}
	expected := object{"relations": []any{object{"attributes": []any{object{"default": tmpl}}}}}
	if compareDescriptorValue(actual, expected, names, "") != nil {
		t.Fatal("nested attribute default rejected")
	}
	for _, field := range []string{"prosrc", "name", "identity", "default", "qual", "action"} {
		if compareDescriptorValue(object{field: text}, object{field: tmpl}, names, "") != ErrCatalog {
			t.Fatalf("unreviewed template field %s accepted", field)
		}
	}
	if compareDescriptorValue([]any{text}, []any{tmpl}, names, ".expression") != ErrCatalog {
		t.Fatal("array element inherited the parent deparse field")
	}
	if compareDescriptorValue(text, tmpl, names, ".relations.attributes..default") != nil ||
		compareDescriptorValue(text, tmpl, names, ".relations.default") != ErrCatalog {
		t.Fatal("legacy path entry changed attribute boundary")
	}
	if compareDescriptorValue(object{"prosrc": text}, object{"prosrc": text}, names, "") != nil ||
		compareDescriptorValue(object{"expression": `literal 'application.safe(x)'`}, object{"expression": text}, names, "") != ErrCatalog {
		t.Fatal("literal text changed during comparison")
	}
}
