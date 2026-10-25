# File storage

The default driver is `local`. The native `s3` driver uses the existing MinIO
SDK and a pre-provisioned bucket. It supports conditional immutable PUT,
streaming SHA-256 checksums, range reads, native multipart reconciliation,
conditional multipart completion, server copy, and incomplete-write reclamation.
It does not buffer a whole object or spool to disk. Credentials stay in deployment
configuration or credential files. Request contexts are bounded to 60 seconds.

```yaml
files:
  backend: s3
  options:
    endpoint: https://storage.example.test
    region: us-east-1
    bucket: deployment-files
    access_key_file: /run/secrets/storage-access-key
    secret_key_file: /run/secrets/storage-secret-key
    sse: AES256
  read_backends:
    local:
      driver: local
      options:
        root: data/files
```

Optional S3 settings: `prefix` (a service-key prefix without a trailing slash),
`sse: none`, or `sse: aws:kms` with `sse_kms_key_id`. Inline `access_key` and
`secret_key` are supported, mutually exclusive with their respective files.
Driver option maps are redacted from the configuration inspector. Bucket
creation, credentials, public-access rules, and lifecycle policies belong to
deployment provisioning. Unsupported conditional writes fail; there is no
unsafe overwrite fallback.

Existing objects are read through their persisted `backend_id`, including
configured legacy identities. File authorization, private references and
resource bindings continue to apply before opening bytes. New objects use the
active backend. Keep legacy identities configured until migration is complete.

```sh
7link-api files migrate-object --config config.yaml \
  --object-id 9007199254740993 --from-backend local
```

This local operator command uses the existing prepared-maintenance lifecycle:
schema validation, normal module initialization and audit/ID infrastructure,
without HTTP listeners or business workers. It verifies source length/SHA-256,
streams the copy, rereads and verifies the destination, then atomically changes
the object backend/location with a version CAS and system audit entry. File
versions and bindings do not change. A conflicting destination or changed source
fails. Rerunning after a lost response verifies the destination and reports
`replayed: true`; no duplicate audit entry is written. An interrupted copy is
verified before reuse. Retained source bytes are never removed by this command;
automated seven-day source garbage collection is not provided.

Tests using a disposable real S3-compatible environment:

```sh
FILES_S3_TEST_ENDPOINT=http://127.0.0.1:19000 \
FILES_S3_TEST_ACCESS_KEY=<test-key> FILES_S3_TEST_SECRET_KEY=<test-secret> \
go test -p 1 ./internal/files -run 'TestS3|TestObjectMigration' -count=1
```

The real tests use the isolated `files-lane-t-acceptance` bucket and delete their
own object keys. Do not point them at a production bucket or credentials.
