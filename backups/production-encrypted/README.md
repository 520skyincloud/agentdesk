# Encrypted Production Database Snapshot

This directory contains an encrypted MySQL snapshot supplied with the
`fc6ea89ff791c16478aa1ac48b92951b77d93fdd` source package.

The repository is public, so the production dump must never be committed in
plaintext. The decryption key is stored and transferred separately.

## Snapshot

- File: `agentdesk-production-20260820-072632.sql.gz.enc`
- Source database: `cs_ai_agent_ai_billing_4db7993`
- Migration version: `67`
- Plaintext SHA-256:
  `0ce0e9ef7a02ed674f815214b88ec898ca1db4c1a95206e855eaa1dedee4934d`
- Encrypted SHA-256:
  `809bbb2f856f03e0683504113f4cce7fbb91890dc9ae6f16e16ff949df6c2915`
- Encryption: AES-256-CBC, PBKDF2-HMAC-SHA-256, 600000 iterations

## Decrypt

Keep the key outside the repository and restrict it to the database operator.

```bash
openssl enc -d \
  -aes-256-cbc \
  -pbkdf2 \
  -iter 600000 \
  -md sha256 \
  -pass file:/secure/path/agentdesk-production-20260820-072632.database-key.txt \
  -in agentdesk-production-20260820-072632.sql.gz.enc \
  -out agentdesk-production-20260820-072632.decrypted.sql.gz

gzip -t agentdesk-production-20260820-072632.decrypted.sql.gz
shasum -a 256 agentdesk-production-20260820-072632.decrypted.sql.gz
```

The final SHA-256 must match the plaintext checksum above.

Do not publish, commit, or send the decrypted dump through chat. It contains
production customer, message, session, WeCom instance, and model credential
records.
