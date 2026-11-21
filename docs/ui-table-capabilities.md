# Table capability contract

HuntDeck uses different table patterns for mutable lists, ranked lists and
read-only summaries. Every mutable list has a complete selection path, while a
read-only summary keeps the interaction surface focused on reading.

| Surface | Selection | Sort/filter | Copy/export | Row actions |
| --- | --- | --- | --- | --- |
| Programs | Select all, selected count, bulk ignore | Sort links, filters, saved views | CSV and copy selected | Open, ignore, notes |
| Targets | Select all, selected count | Sort links, filters, saved views | Plain text and copy selected | Program/open links |
| Disclosed | Select all, selected count | Sort links, filters | Export and copy selected links | Open report |
| Hunt board | Select all, selected count | Column sort, views | Copy selected targets | Open and copy |
| API tokens | Select all, selected count | State and timestamps | — | Revoke, rotate, delete, bulk revoke/delete |
| MCP keys | Select all, selected count | State and timestamps | — | Revoke, delete, bulk delete, cleanup unused |
| Scope | — | Search, scope/type/time filters | Copy targets and text export | Expand notes |
| Changes | — | Feed filters and pagination | Feed and text export | Open detail |
| Queue and stats | — | Page or chart controls | Page-specific exports where offered | Open target/report |

The contract is checked by `internal/web/tablecapabilities_test.go`. A new
mutable table must add its selection, status feedback and bulk action path to
that test before it is considered complete.
