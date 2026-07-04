# Metrics

takuhai and crawler services expose Prometheus metrics at `/metrics`.

## takuhai

| Metric | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `takuhai_build_info` | gauge | `version`, `commit` | Build metadata; value is always `1`. |
| `takuhai_http_requests_total` | counter | `method`, `path`, `status` | HTTP requests by routed path. Unknown paths use `path="other"`. |
| `takuhai_http_request_duration_seconds` | histogram | `method`, `path` | HTTP request duration. Streamable MCP `GET /mcp` sessions are excluded. |
| `takuhai_ingest_batches_total` | counter | `result` | Ingest batches by `ok` or `error`. |
| `takuhai_ingest_posts_total` | counter | `source`, `result` | Ingested posts by source and outcome. Results include `new`, `updated`, `duplicate`, `conflict`, `skipped`, and `error`. |
| `takuhai_ingest_batch_size` | histogram | none | Posts per ingest batch. |
| `takuhai_queue_items` | gauge | `state` | Current release queue/status counts. States are `claimable`, `leased`, `unmatched`, `matched`, `suppressed`, and `exhausted`. |
| `takuhai_queue_stats_scrape_ok` | gauge | none | `1` when queue stats were readable during scrape, otherwise `0`. |
| `takuhai_catalog_raw_posts` | gauge | none | Current row count in `raw_items`. |
| `takuhai_catalog_infohashes` | gauge | none | Current row count in `releases`; one row per unique infohash. |
| `takuhai_catalog_refs` | gauge | none | Current number of unique non-empty refs. |
| `takuhai_catalog_stats_scrape_ok` | gauge | none | `1` when catalog stats were readable during scrape, otherwise `0`. |
| `takuhai_queue_claims_total` | counter | `result` | Queue claim requests by `claimed`, `empty`, or `error`. |
| `takuhai_queue_claimed_items_total` | counter | none | Claimed queue items. |
| `takuhai_queue_claim_batch_size` | histogram | none | Items per non-empty claim response. |
| `takuhai_submit_total` | counter | `status`, `result` | Submit attempts by matcher status and result. Status is `matched`, `unmatched`, `suppressed`, or `invalid`; result is `ok`, `conflict`, or `error`. |
| `takuhai_submit_confidence` | histogram | `status` | Confidence values for successful `matched` and `suppressed` submissions. |
| `takuhai_mcp_tool_calls_total` | counter | `tool`, `result` | MCP tool calls by tool and `ok`/`error`. |
| `takuhai_mcp_tool_duration_seconds` | histogram | `tool` | MCP tool call duration; use this for MCP latency. |
| `takuhai_mcp_resolve_magnets_infohashes_total` | counter | `result` | `resolve_magnets` infohash lookups by `hit` or `miss`. |

## DMHY Crawler

| Metric | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `takuhai_dmhy_build_info` | gauge | `version`, `commit` | Build metadata; value is always `1`. |
| `takuhai_dmhy_http_requests_total` | counter | `method`, `path`, `status` | HTTP requests by routed path. Unknown paths use `path="other"`. |
| `takuhai_dmhy_http_request_duration_seconds` | histogram | `method`, `path` | HTTP request duration. |
| `takuhai_dmhy_crawl_requests_total` | counter | `result` | Crawl requests by `ok`, `bad_request`, `fetch_error`, `parse_error`, or `error`. |
| `takuhai_dmhy_crawl_duration_seconds` | histogram | none | Crawl request duration. |
| `takuhai_dmhy_crawl_pages_fetched_total` | counter | none | Successfully fetched archive pages. |
| `takuhai_dmhy_crawl_posts_returned_total` | counter | none | Posts returned by crawl responses. |
| `takuhai_dmhy_crawl_posts_per_request` | histogram | none | Posts returned per crawl request. |
| `takuhai_dmhy_fetch_requests_total` | counter | `result` | Upstream page fetches by `ok` or `error`. Rate-limit waiting time is not included. |
| `takuhai_dmhy_fetch_duration_seconds` | histogram | none | Upstream page fetch duration, after rate-limit waiting. |
| `takuhai_dmhy_parse_posts_total` | counter | `result` | Parsed posts by result. Currently only successful parsed posts increment `result="ok"`. |

## Nyaa Crawler

The Nyaa crawler exposes the same metric families under the `takuhai_nyaa_*`
namespace.

Go runtime and process metrics are also exported by each service.

An example Grafana dashboard JSON is available at
[`docs/grafana/takuhai-dashboard.json`](grafana/takuhai-dashboard.json). It uses a
Prometheus datasource variable named `datasource`.
