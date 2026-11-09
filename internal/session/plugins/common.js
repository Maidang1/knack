// Streaming protocol v1. Pure record transforms: no I/O and no retained state.
function objectList(v) {
  return Array.isArray(v) ? v.filter(b => b && typeof b === 'object' && !Array.isArray(b)) : [];
}
function stringValue(v) { return typeof v === 'string' ? v : ''; }
function tokenCount(v) { return typeof v === 'number' && Number.isInteger(v) ? Math.max(0, v) : 0; }
function text(v) {
  if (typeof v === 'string') return v;
  if (!Array.isArray(v)) return '';
  return objectList(v).flatMap(b => {
    if (['text','input_text','output_text'].includes(b.type)) return [stringValue(b.text)];
    if (['image','input_image'].includes(b.type)) return ['[图片]'];
    if (b.type === 'tool_result') return [text(b.content)];
    return stringValue(b.text) ? [b.text] : [];
  }).join('\n');
}
function content(m) {
  return (typeof m.content === 'string' && m.content.trim() !== '') ||
    (Array.isArray(m.content) && objectList(m.content).some(b => ['text','image','input_text'].includes(b.type)));
}
function userKind(t) {
  return ['<system-reminder>','<local-command-caveat>','<local-command-stdout>','<local-command-stderr>','Caveat: '].some(p => t.trim().startsWith(p)) ? 'context' : 'user';
}
function injected(t) {
  return ['<environment_context>','<user_instructions>','# AGENTS.md','<permissions','<turn_aborted>','<skill>','<subagent_notification>'].some(p => t.trim().startsWith(p));
}
function usage(u, inclusive) {
  u = u || {};
  const read = tokenCount(u[inclusive ? 'cached_input_tokens' : 'cache_read_input_tokens']);
  const write = tokenCount(u[inclusive ? 'cache_write_input_tokens' : 'cache_creation_input_tokens']);
  return {Input: Math.max(0, tokenCount(u.input_tokens) - (inclusive ? read + write : 0)), Output: tokenCount(u.output_tokens), CacheRead: read, CacheWrite: write};
}
function known(u) { return !!u && Object.prototype.hasOwnProperty.call(u,'input_tokens') && Object.prototype.hasOwnProperty.call(u,'output_tokens'); }
function toolName(p) {
  const n = stringValue(p.name), ns = stringValue(p.namespace);
  return ns.startsWith('mcp__') && n && !n.startsWith('mcp__') ? ns + '__' + n : n;
}
function record(o, mode) {
  // Do not serialize conversation replay during background statistics scans.
  const sink = []; sink.push = function () {};
  return {timestamp:o.timestamp || '', type:o.type || '', id:o.uuid || '', stats:mode === 'replay' ? sink : [], replay:mode === 'stats' ? sink : []};
}
function event(r, name, body, error) { r.replay.push({op:'entry', kind:'event', name, text:body || '', error:!!error}); }
function user(r, kind, body) {
  if (body.trim().startsWith('[Request interrupted by user')) event(r,'已中断','用户中断了请求',true);
  else r.replay.push({op:'entry',kind,text:body});
}
function assistant(r, bs, rid, model) {
  bs.forEach(b => {
    if (['thinking','redacted_thinking'].includes(b.type)) r.replay.push({op:'entry',kind:'thinking',rid,name:model,text:b.thinking || ''});
    if (b.type === 'text' && stringValue(b.text).trim()) r.replay.push({op:'entry',kind:'assistant',rid,name:model,text:b.text});
    if (b.type === 'tool_use') r.replay.push({op:'entry',kind:'tool',rid,id:b.id,name:b.name,input:b.input});
  });
}
