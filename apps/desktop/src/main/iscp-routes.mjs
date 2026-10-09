import registry from '../shared/iscp-operations.json' with {type:'json'};
export { registry as ISCP_REGISTRY };
const ID=/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$/u;

export function mapV2Route(url,method,body) {
  if(url.hash||url.username||url.password)throw new Error('ISCP backend path is invalid');
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
      for(const [key,value] of url.searchParams){if(!operation.params.includes(key)||Object.hasOwn(params,key)||value.length>4096||/[\x00-\x1f]/u.test(value))throw new Error('ISCP query is invalid');params[key]=value;}
      return {operation:operation.name,params,...(body===undefined?{}:{body})};
    }
  }
  throw new Error('This capability is unavailable through ISCP');
}
export function requireOperation(name){const operation=registry.find((row)=>row.name===name&&row.direction==='forward');if(!operation)throw new Error('ISCP operation is unavailable');return operation;}
