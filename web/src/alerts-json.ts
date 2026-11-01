// Bounded strict JSON boundary: object duplicate keys (including escapes), invalid
// Unicode and trailing documents must be rejected before DTO validation.
export function parseAlertJSON(raw:string):unknown {
  if(new TextEncoder().encode(raw).length>262144)throw new Error('告警响应超过大小上限。')
  let at=0
  const bad=():never=>{throw new Error('告警响应不是严格 JSON。')},white=()=>{while(/[\t\r\n ]/.test(raw[at]||'!'))at++}
  function string():string {const start=at++;while(at<raw.length){const c=raw[at++];if(c==='"'){let value:unknown;try{value=JSON.parse(raw.slice(start,at))}catch{return bad()}if(typeof value!=='string'||[...value].some(c=>{const n=c.codePointAt(0)!;return n>=0xd800&&n<=0xdfff}))return bad();return value}if(c==='\\')at++;}return bad()}
  function value(depth:number):unknown {
    if(depth>12)return bad();white();const c=raw[at]
    if(c==='"')return string()
    if(c==='{'){at++;white();const result:Record<string,unknown>=Object.create(null);if(raw[at]==='}'){at++;return result}while(true){white();if(raw[at]!=='"')return bad();const key=string();if(Object.hasOwn(result,key))return bad();white();if(raw[at++]!==':')return bad();result[key]=value(depth+1);white();const sep=raw[at++];if(sep==='}')return result;if(sep!==',')return bad()}}
    if(c==='['){at++;white();const result:unknown[]=[];if(raw[at]===']'){at++;return result}while(true){result.push(value(depth+1));white();const sep=raw[at++];if(sep===']')return result;if(sep!==',')return bad()}}
    for(const [literal,out]of [['true',true],['false',false],['null',null]] as const){if(raw.startsWith(literal,at)){at+=literal.length;return out}}
    const number=/^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?/.exec(raw.slice(at));if(!number)return bad();at+=number[0].length;const n=Number(number[0]);if(!Number.isSafeInteger(n)||!/^-(?:[1-9][0-9]*)$|^(?:0|[1-9][0-9]*)$/.test(number[0]))return bad();return n
  }
  const result=value(0);white();if(at!==raw.length)return bad();return result
}
