knack.registerAgent({
  info:{ID:'codex',Name:'Codex',Skills:true,RateNote:'无法可靠关联请求起止时间，tok/s 暂不支持',TimingNote:'Codex 请求耗时按输入完成到用量记录的间隔估算；旧日志缺少逐请求用量时不计为零。'},
  roots:['.codex/sessions','.codex/archived_sessions'],suffixes:['.jsonl','.jsonl.zst'],
  visit() { return {ok:true}; }, parent() { return ''; },
  // Read only simple, unescaped fields from the bounded prefix. Never retain
  // replacement_history, attachments or other conversation content.
  readHeader(head) {
    function field(name) {
      const key = '"' + name + '":"', i = head.indexOf(key);
      if (i < 0) return '';
      const rest = head.slice(i + key.length), j = rest.indexOf('"');
      return j < 0 || rest.slice(0,j).includes('\\') ? '' : rest.slice(0,j);
    }
    const type = field('type'), timestamp = field('timestamp');
    if (!timestamp) return null;
    if (type === 'response_item' && head.includes('"payload":{"type":"message"')) {
      const role = field('role');
      if (role) return {timestamp,type,payload:{type:'message',role}};
    }
    return type === 'compacted' ? {timestamp,type} : null;
  },
  readRecord(o, mode) {
    const r = record(o, mode), s = r.stats, p = o.payload || {};
    if (o.type === 'session_meta') {
      s.push({op:'usageScope',scope:p.id || p.session_id || ''},{op:'known'},{op:'metadata',id:p.id || p.session_id,cwd:p.cwd || '',branch:(p.git || {}).branch || '',replace:true,identityOnce:true,parent:p.parent_thread_id || ((((p.source || {}).subagent || {}).thread_spawn || {}).parent_thread_id || '')});
      const body = typeof p.base_instructions === 'string' ? p.base_instructions : (p.base_instructions || {}).text || '';
      if (body.trim()) r.replay.push({op:'entry',kind:'context',name:'系统提示词',text:body,once:true});
    } else if (o.type === 'turn_context') {
      s.push({op:'model',model:p.model || ''}); r.replay.push({op:'model',model:p.model || ''});
    } else if (o.type === 'token_usage_record') {
      s.push({op:'request',id:p.response_id || '',unique:true,inputTiming:true,correlateUsage:true,tokens:usage(p.usage,true),known:known(p.usage)});
      r.replay.push({op:'associate',rid:p.response_id || ''});
    } else if (o.type === 'compacted') { s.push({op:'event',kind:'compact'}); event(r,'上下文压缩'); }
    else if (o.type === 'event_msg') {
      if (p.type === 'token_count' && p.info && p.info.last_token_usage) {
        const last = p.info.last_token_usage, tokens = usage(last,true), total = p.info.total_token_usage;
        if (tokens.Input + tokens.Output + tokens.CacheRead + tokens.CacheWrite > 0) {
          // Cumulative values identify reports; only last_token_usage is counted.
          const key = total ? ['input_tokens','cached_input_tokens','cache_write_input_tokens','output_tokens','reasoning_output_tokens'].map(k => tokenCount(total[k])).join(':') : (o.timestamp || '') + ':' + JSON.stringify(tokens);
          s.push({op:'known'},{op:'fallbackUsage',key,tokens,total:usage(total,true),known:known(last)});
        }
      }
      if (p.type === 'item_completed') {
        const from = p.started_at_ms || 0, to = p.completed_at_ms || 0, kind = (p.item || {}).type || '';
        if (from > 0 && to > from && kind === 'Reasoning') s.push({op:'think',duration:to-from});
        if (from > 0 && to >= from) r.replay.push({op:'timing',kind,from,to});
      }
      if (p.type === 'task_started') s.push({op:'input',resetPrompt:true},{op:'begin',id:p.turn_id || '',unique:true});
      if (['task_complete','turn_aborted'].includes(p.type)) s.push({op:'finish',id:p.turn_id || '',duration:p.duration_ms,interrupted:p.type === 'turn_aborted'});
      if (p.type === 'turn_aborted') event(r,'已中断','',true);
      if (p.type === 'error') event(r,'错误',p.message,true);
    } else if (o.type === 'response_item') {
      if (p.type === 'message') {
        const body = text(p.content);
        if (p.role === 'assistant') { s.push({op:'reply',force:true}); r.replay.push({op:'entry',kind:'assistant',text:body,pending:true,measured:'AgentMessage',currentModel:true}); }
        if (p.role === 'user') { s.push({op:'input'}); if (body.trim() && !body.trim().startsWith('<') && !body.trim().startsWith('# AGENTS.md')) s.push({op:'prompt',text:body}); r.replay.push({op:'entry',kind:injected(body) ? 'context' : 'user',text:body}); }
        if (['developer','system'].includes(p.role)) r.replay.push({op:'entry',kind:'context',text:body});
      } else if (p.type === 'reasoning') r.replay.push({op:'entry',kind:'thinking',text:objectList(p.summary).map(x => stringValue(x.text)).join('\n\n'),pending:true,measured:'Reasoning',currentModel:true});
      else if (['function_call','custom_tool_call','local_shell_call','web_search_call','tool_search_call'].includes(p.type)) {
        const raw = p.arguments == null ? p.input : p.arguments;
        if (['function_call','custom_tool_call'].includes(p.type)) s.push({op:'use',id:p.call_id,name:toolName(p),input:raw,detectSkills:true});
        r.replay.push({op:'entry',kind:'tool',id:p.call_id || p.id || '',name:toolName(p) || p.type.replace(/_call$/,''),input:(typeof p.arguments === 'string' && p.arguments) || (typeof p.input === 'string' && p.input) || (p.action == null ? raw : p.action),pending:true});
      } else if (['function_call_output','custom_tool_call_output','local_shell_call_output','tool_search_output'].includes(p.type)) {
        if (['function_call_output','custom_tool_call_output'].includes(p.type)) s.push({op:'result',id:p.call_id,output:text(p.output)},{op:'input'});
        r.replay.push({op:'result',id:p.call_id,text:text(p.output)});
      }
    }
    return r;
  }
});
