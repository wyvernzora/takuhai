Resolve infohashes into stored magnet URIs.

Input is `{ "infohashes": ["..."] }`.

Response is `{ "magnets": { "<infohash>": "<magnet-uri>" } }`.

Unknown infohashes are omitted from `magnets`. The result may be empty. Returned magnets are the full magnet URIs stored from ingest; copy them verbatim when queueing downloads.
