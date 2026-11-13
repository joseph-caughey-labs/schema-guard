# schema-guard

[![CI](https://github.com/joseph-caughey-labs/schema-guard/actions/workflows/ci.yml/badge.svg)](https://github.com/joseph-caughey-labs/schema-guard/actions/workflows/ci.yml)

Compares two versions of a JSON Schema and tells you which changes break someone.

Whether a change breaks depends on which way the data flows. Adding `"refunded"`
to an order's status enum is harmless for a request body, because no existing
client sends it. In a response it breaks every client whose `switch` doesn't
have that case. schema-guard asks which side you're protecting and classifies
each change for that side.

| mode | protects | breaking examples |
| --- | --- | --- |
| `output` | readers of data you produce (API responses, events) | property removed, field can now be `null`, new enum value, `integer` widened to `number` |
| `input` | senders of data you accept (request bodies, config) | required property added, enum value removed, `maxLength` reduced, unknown properties now rejected |
| `both` (default) | data that flows both ways | anything breaking in either |

Each change is reported as `breaking`, `non-breaking`, or `review`. Review is for
changes that can't be classified mechanically: a changed regex, a different
`format`, an edit inside `oneOf`, or a loosened bound on output.

## Install

```bash
go install github.com/joseph-caughey-labs/schema-guard/cmd/schema-guard@latest
```

## Usage

```bash
schema-guard [flags] OLD NEW
```

Flags go before the file names.

| flag | default | |
| --- | --- | --- |
| `-mode` | `both` | `output`, `input` or `both` |
| `-pointer` | whole file | JSON Pointer to the schema in each file, e.g. `/components/schemas/Order` |
| `-format` | `text` | `text` or `json` |
| `-strict` | off | treat `review` changes as breaking |

The exit status is 0 when there are no breaking changes, 1 when there are, and 2
for a usage error, such as an unreadable file or a pointer that doesn't resolve.

The `examples` directory holds two versions of an OpenAPI document:

```
$ schema-guard -mode output -pointer /components/schemas/Order examples/orders-v1.json examples/orders-v2.json
breaking      $.lines[].quantity  can now be number
breaking      $.note              can now be null
review        $.note              maxLength 200 -> 500 (loosened)
non-breaking  $.placedAt          optional property added
breaking      $.status            new allowed value "refunded"

3 breaking, 1 review, 1 non-breaking (mode: output)
```

The same comparison in `-mode input` reports five non-breaking changes and exits
0. Every change in v2 widens what is accepted.

## In CI

Compare the schema on a pull request with the one on `main`:

```yaml
- uses: actions/checkout@v7
  with:
    fetch-depth: 0
- uses: actions/setup-go@v7.0.0
  with:
    go-version: stable
- run: git show origin/main:api/openapi.json > /tmp/openapi-main.json
- run: >
    go run github.com/joseph-caughey-labs/schema-guard/cmd/schema-guard@latest
    -mode output -pointer /components/schemas/Order
    /tmp/openapi-main.json api/openapi.json
```

## What it understands

- `type`, including type arrays and OpenAPI 3.0 `nullable`. An `integer` is
  also a `number`.
- `properties`, `required` and `additionalProperties: false`
- `items` for arrays, compared recursively
- `enum` and `const`
- `minimum`, `maximum`, `exclusiveMinimum`, `exclusiveMaximum`, `minLength`,
  `maxLength`, `minItems`, `maxItems`, `minProperties` and `maxProperties`
- `pattern` and `format`
- Local `$ref` values (`#/...`), including recursive schemas. A schema shared
  through `$ref` is reported at every path that uses it.

`allOf`, `anyOf`, `oneOf`, `not` and tuple-form `items` aren't analysed. If they
change, the change is reported for review. Remote `$ref` values are rejected.
Keywords next to a `$ref` are ignored.

## As a library

```go
before, err := schemadiff.LoadFile("v1.json", "/components/schemas/Order")
// ...
after, err := schemadiff.LoadFile("v2.json", "/components/schemas/Order")
// ...
for _, c := range schemadiff.Compare(before, after, schemadiff.Output) {
	fmt.Println(c.Severity, c.Path, c.Message)
}
```

It uses only the standard library and needs Go 1.24 or later.

## License

MIT
