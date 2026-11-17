export interface PlatformNavigationGroup { readonly id:string; readonly title:string; readonly paths:readonly string[] }
export const platformNavigationGroups:readonly PlatformNavigationGroup[]=[
  {id:'access',title:'客户与权限',paths:['/platform/tenants','/platform/users']},
  {id:'channels',title:'渠道与定价',paths:['/platform/channels','/platform/catalog','/platform/prices','/platform/price-groups','/platform/cost-versions']},
  {id:'funds',title:'资金与结算',paths:['/platform/credits','/platform/wallet-requests','/platform/payments','/platform/vouchers','/platform/wallet-sources','/platform/refunds','/platform/cash-refunds','/platform/reconciliations','/platform/procurement','/platform/periodic-budget']},
  {id:'operations',title:'运营与审计',paths:['/platform/alerts','/platform/reports','/platform/recovery','/platform/audit']},
]
export function groupPlatformLinks<T extends {path:string}>(links:readonly T[]):{id:string;title:string;items:T[]}[]{
  const known=new Set<string>(),seen=new Set<string>()
  for(const group of platformNavigationGroups){for(const path of group.paths){if(known.has(path))throw new Error(`重复的平台导航分组：${path}`);known.add(path)}}
  for(const link of links){if(!known.has(link.path)||seen.has(link.path))throw new Error(`未分组或重复的平台导航：${link.path}`);seen.add(link.path)}
  return platformNavigationGroups.map(group=>({id:group.id,title:group.title,items:links.filter(link=>group.paths.includes(link.path))})).filter(group=>group.items.length>0)
}
export function platformGroupForPath(path:string):string{return platformNavigationGroups.find(group=>group.paths.includes(path))?.id||''}
export function parseNavigationGroupState(raw:string|null):Record<string,boolean>{
  if(!raw||raw.length>2000)return{}
  try{const value=JSON.parse(raw);if(!value||typeof value!=='object'||Array.isArray(value))return{};const known=new Set(platformNavigationGroups.map(group=>group.id)),state:Record<string,boolean>={};for(const[id,expanded]of Object.entries(value)){if(known.has(id)&&typeof expanded==='boolean')state[id]=expanded}return state}catch{return{}}
}
export function serializeNavigationGroupState(value:Record<string,boolean>):string{const known=new Set(platformNavigationGroups.map(group=>group.id)),safe:Record<string,boolean>={};for(const[id,expanded]of Object.entries(value)){if(known.has(id)&&typeof expanded==='boolean')safe[id]=expanded}return JSON.stringify(safe)
}
