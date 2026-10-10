import registry from '../shared/iscp-operations.json' with {type:'json'};
export { registry as ISCP_REGISTRY };
const ID=/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$/u;

export function mapV2Route(url,method,body) {
  if(url.hash||url.username||url.password)throw new Error('ISCP backend path is invalid');
  // The original composer uses item GET/PUT while the canonical transport
  // keeps draft snapshots and saves on the collection operations.
  const draft = /^\/api\/email\/drafts\/([A-Za-z0-9][A-Za-z0-9_.:-]{0,159})$/u.exec(url.pathname);
  if (draft && ['GET', 'PUT'].includes(method)) {
    if (url.search || method === 'GET' && body !== undefined || method === 'PUT' && (!body || typeof body !== 'object' || Array.isArray(body) || body.id !== undefined && body.id !== draft[1])) throw new Error('ISCP draft request is invalid');
    return { operation: requireOperation(method === 'GET' ? 'mail.drafts.list' : 'mail.drafts.save').name, params: { draft: draft[1] }, ...(method === 'PUT' ? { body: { ...body, id: draft[1] } } : {}) };
  }
  const segments=url.pathname.split('/');
  for(const operation of registry){
    if(!operation.http||operation.http.method!==method||operation.direction!=='forward')continue;
    const template=operation.http.path.split('/');
    if(template.length!==segments.length)continue;
    const params={};let matched=true;
    for(let i=0;i<template.length;i++){
      if(template[i].startsWith('{')&&template[i].endsWith('}')){
        let value;try{value=decodeURIComponent(segments[i]);}catch{matched=false;break;}
        if(!ID.test(value)){matched=false;break;}params[template[i].slice(1,-1)]=value;
      }else if(template[i]!==segments[i]){matched=false;break;}
    }
    if(matched){
      const targetIDs=[];
      for(const [key,value] of url.searchParams){
        if (operation.name === 'mail.presentations.get' && key === 'target_id') {
          if (!ID.test(value) || targetIDs.length >= 100 || targetIDs.includes(value)) throw new Error('ISCP query is invalid');
          targetIDs.push(value); continue;
        }
        if(!operation.params.includes(key)||Object.hasOwn(params,key)||Buffer.byteLength(value)>1024||/[\x00-\x1f]/u.test(value) || operation.name === 'mail.presentations.get' && key === 'target_ids')throw new Error('ISCP query is invalid');params[key]=value;
      }
      if (targetIDs.length) params.target_ids = JSON.stringify(targetIDs);
      const name = operation.name === 'mail.providers.update' && body && Object.hasOwn(body, 'intake_enabled') ? requireOperation('mail.intake.update').name : operation.name;
      return {operation:name,params,...(body===undefined?{}:{body})};
    }
  }
  throw new Error('This capability is unavailable through ISCP');
}
export function requireOperation(name){const operation=registry.find((row)=>row.name===name&&row.direction==='forward');if(!operation)throw new Error('ISCP operation is unavailable');return operation;}
