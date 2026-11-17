export interface ReleaseLaneToken { readonly epoch:number; readonly lane:'list'|'detail'; readonly serial:number }
export function releaseNextHistory(history:string[],current:string):string[]{return[...history,current]}
export function releasePreviousHistory(history:string[]):{cursor:string;history:string[]}{const next=[...history],cursor=next.pop()||'';return{cursor,history:next}}
export function createReleaseQualificationScope(){
  let epoch=0,list=0,detail=0
  function reset(){epoch++;list=0;detail=0}
  function ticket(lane:'list'|'detail'):ReleaseLaneToken{const serial=lane==='list'?++list:++detail;return{epoch,lane,serial}}
  function accepts(token:ReleaseLaneToken):boolean{return token.epoch===epoch&&token.serial===(token.lane==='list'?list:detail)}
  return{reset,ticket,accepts}
}
