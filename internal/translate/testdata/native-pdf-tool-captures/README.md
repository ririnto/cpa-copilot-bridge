# Native PDF view-tool inference captures

These five fixtures contain genuine forwarded native Copilot CLI inference requests and complete HTTP 200 SSE responses recorded on 2026-10-09 UTC.
The GPT pair captures the initial native `view` call and the next model request containing its paired function call and function output.
The Gemini pair captures the initial native `view` call and the next model request containing its paired assistant tool call and tool message.
The fifth capture is Gemini's same-session text-only extra user turn with the actual preceding tool and answer history retained.
GPT's extra user turn remains NOTRUN because the first harness incorrectly stopped on an ACP notification with a null name, while the two captured upstream inference requests establish the native view and model follow-up.

The initial requests retain the original local `tagged_files` route rather than a projected inline PDF attachment.
The actual native view results retain all 623 bytes of the unchanged PDF canary, whose SHA256 is `e8627ac30088e8d989b1e265018ac32bc5b7b94bb4fdb51954deaff0c3ff2d68`.
Those results are raw PDF text produced by the native file reader, including its PDF syntax and Q7B9 marker, rather than output from a substitute parser.
Both tool-result model follow-ups identify Q7B9, and Gemini's extra user turn identifies it from the retained conversation history.

Request field topology, options, tool schemas, call/result structure, and content order are preserved.
Private local paths and native resource basenames are replaced consistently under `/sanitized/native-attachments/`.
Instructions, environment text, tool descriptions, and human-readable reasoning text are replaced with explicit placeholders while retaining their fields.
Dynamic identifiers and opaque signed or encrypted blobs use consistent placeholders across all five flows, preserving the equality relationships of calls, results, and history.
The captured GPT client replaces the function item's `id` in its continuation request while retaining the original `call_id`, and the fixtures preserve that distinction and paired result identity.
Persistent safety identifiers use the explicit portable string sentinel `SANITIZED_SAFETY_IDENTIFIER_001`.
Streamed arguments and reasoning are sanitized as logical concatenated values before redistribution across the original delta events, retaining every event and its order.
The SSE files retain all original framing and final blank-line delimiters after terminal events or done markers, so patch whitespace warnings must not cause these delimiters to be removed.
Capture headers, credentials, raw private paths, private run identifiers, and actual machine/session identifiers are excluded.
No original private capture is modified.

The tests replay the original native request with only existing model/stream normalization and compare complete native response event fields and order, permitting existing Chat object normalization.
They verify exact native models, terminal states, marker answers, view argument paths, call/result identities, PDF byte hashes, and Gemini's real extra user turn history.
There are no derived JSON response files or projected request fixtures in this set.
Sanitized fixtures cannot prove transport fingerprint parity, signed-blob validity, live cross-protocol PDF support, upload behavior, or provider-side document parsing independent of the client's native view tool.
