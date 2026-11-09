knack.registerAgent({
  info: {ID:'claude',Name:'Claude',Skills:true,OutputRate:true,PermissionWait:true,ToolOutcome:true},
  roots:['.claude/projects'], suffixes:['.jsonl'],
  visit(parts, dir) {
    if (dir) return {ok:parts.length !== 3 || parts[2] === 'subagents'};
    if (parts.length === 2) return {ok:true};
    if (parts.length === 4 && parts[2] === 'subagents') return {ok:true,parent:parts[1],child:parts[3].replace(/^agent-/,'').replace(/\.jsonl$/,'')};
    return {ok:false};
  },
  parent(path,parent) { return path.split('/').slice(0,-3).join('/') + '/' + parent + '.jsonl'; },
  readRecord(o, mode) {
    const r = record(o, mode), s = r.stats, m = o.message || {}, bs = objectList(m.content);
    s.push({op:'metadata',id:o.sessionId,cwd:o.cwd,branch:o.gitBranch,title:o.aiTitle,preserveChild:true});
    if (o.type === 'user') {
      s.push({op:'known'});
      if (bs.some(b => b.type === 'tool_result')) {
        bs.filter(b => b.type === 'tool_result').forEach(b => {
          s.push({op:'result',id:b.tool_use_id, result:o.toolUseResult,permission:o.permissionDecision,error:!!b.is_error,output:text(b.content)});
          r.replay.push({op:'result',id:b.tool_use_id,text:text(b.content),error:!!b.is_error});
        });
      } else {
        if (o.isMeta) {
          if (o.sourceToolUseID || o.turnCompanion) s.push({op:'skillContent',id:o.sourceToolUseID || '',companion:!!o.turnCompanion,text:text(m.content)});
        } else if (!o.isCompactSummary && content(m)) {
          s.push({op:'begin',id:o.uuid,text:text(m.content),command:true,interruptNotice:text(m.content).trim().startsWith('[Request interrupted by user')});
        }
        if (content(m)) user(r,o.isMeta || o.isCompactSummary ? 'context' : userKind(text(m.content)),text(m.content));
      }
    } else if (o.type === 'assistant') {
      const id = m.id || o.requestRef || o.requestId || o.uuid || '';
      s.push({op:'known'},{op:'request',id,model:m.model || '',tokens:usage(m.usage,false),known:known(m.usage),think:o.thinkingDurationMs || 0,error:!!o.isApiErrorMessage});
      bs.filter(b => b.type === 'tool_use').forEach(b => s.push({op:'use',id:b.id,name:b.name,input:b.input}));
      if (o.isApiErrorMessage) s.push({op:'event',kind:'api_error'},{op:'reply',clear:true});
      else if (!bs.some(b => b.type === 'tool_use')) s.push({op:'reply',force:m.stop_reason === 'end_turn',content:!!content(m)});
      assistant(r,bs,id,m.model || '');
      if (o.isApiErrorMessage) r.replay.push({op:'entry',kind:'event',rid:id,name:'API 请求失败',text:text(m.content),error:true});
    } else if (o.type === 'system') {
      if (o.subtype === 'turn_duration') s.push({op:'duration',duration:o.durationMs});
      if (o.subtype === 'compact_boundary') { s.push({op:'event',kind:'compact',duration:(o.compactMetadata || {}).durationMs}); event(r,'上下文压缩'); }
      if (o.subtype === 'away_summary') { s.push({op:'away'},{op:'event',kind:'away_summary'}); event(r,'离开摘要',o.content); }
      if (o.subtype === 'api_error') event(r,'API 请求失败',o.content,true);
    }
    return r;
  }
});
