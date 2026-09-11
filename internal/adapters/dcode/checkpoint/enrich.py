"""Enrich terminal hook transcripts from DCode's local checkpoint writes."""
import json
import os
from pathlib import Path
import sqlite3
import sys
import tempfile


def enrich(payload):
    from langgraph.checkpoint.serde.jsonplus import JsonPlusSerializer
    from deepagents_code.hooks.transcript import _record_from_message, redact_transcript_value
    sid = payload.get('session_id')
    original = payload.get('transcript_path')
    if not sid or not original:
        return payload
    dbpath = Path.home() / '.deepagents/.state/sessions.db'
    records = {}
    with sqlite3.connect(dbpath.as_uri() + '?mode=ro', uri=True, timeout=2) as db:
        rows = db.execute("SELECT type,value FROM writes WHERE thread_id=? AND checkpoint_ns='' AND channel='messages' ORDER BY checkpoint_id,task_id,idx", (sid,))
        serde = JsonPlusSerializer()
        for kind, value in rows:
            messages = serde.loads_typed((kind, value))
            for message in messages if isinstance(messages, list) else [messages]:
                record = _record_from_message(message, thread_id=sid, agent_id=None, sequence=len(records))
                if record is None:
                    continue
                row = record.model_dump(mode='json', exclude_none=True)
                usage = getattr(message, 'usage_metadata', None)
                if row['role'] == 'assistant' and isinstance(usage, dict):
                    row['usage_metadata'] = usage
                calls = getattr(message, 'tool_calls', None)
                if calls:
                    row['tool_calls'] = redact_transcript_value(calls)
                tool_call_id = getattr(message, 'tool_call_id', None)
                if tool_call_id:
                    row['tool_call_id'] = tool_call_id
                records[row['record_id']] = row
    if not any('usage_metadata' in row for row in records.values()):
        return payload
    target = Path(original)
    if not target.name.endswith('.otel.jsonl'):
        target = target.with_suffix('.otel.jsonl')
    fd, temp = tempfile.mkstemp(prefix='.otel-', dir=target.parent)
    try:
        with os.fdopen(fd, 'w') as out:
            for i, row in enumerate(records.values()):
                row['sequence'] = i
                out.write(json.dumps(row, ensure_ascii=False) + '\n')
        os.replace(temp, target)
    finally:
        if os.path.exists(temp):
            os.unlink(temp)
    return {**payload, 'transcript_path': str(target)}


if __name__ == '__main__':
    payload = json.load(sys.stdin)
    try:
        payload = enrich(payload)
    except Exception:
        pass  # Fail open; never log private transcript data.
    print(json.dumps(payload))
