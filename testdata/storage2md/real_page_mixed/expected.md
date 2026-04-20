# Payment Service Redesign

The payment service handles **card** and *bank* transfers. See [the spec](https://example.com/spec).

## Status

```cfmd-raw
<ac:structured-macro ac:name="status"><ac:parameter ac:name="colour">Green</ac:parameter><ac:parameter ac:name="title">Launched</ac:parameter></ac:structured-macro>
```

## Configuration

```yaml
timeout: 30
retries: 3
```

## Architecture

```cfmd-raw
<ac:image ac:alt="flow" ac:width="600"><ri:attachment ri:filename="flow.png"/></ac:image>
```

## Details

<details>
<summary>Implementation notes</summary>

- Uses **idempotency** keys.
- Retries on 5xx.

</details>

## Callouts

> [!NOTE]
> Launching Q2.

```cfmd-raw
<ac:structured-macro ac:name="warning"><ac:parameter ac:name="title">Rollback needed</ac:parameter><ac:rich-text-body><p>Check the runbook.</p></ac:rich-text-body></ac:structured-macro>
```

## Team

| Name | Role |
|---|---|
| Alice | Eng |
| Bob | PM |
