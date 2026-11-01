<script setup lang="ts">
defineProps<{ eyebrow: string; title: string; description: string }>()
</script>

<template>
  <section class="identity-page">
    <aside class="identity-story"><div class="eyebrow">YOUR AI API WORKSPACE</div><h1>连接模型能力，<br />从自己的账号开始。</h1><p>一个工作空间，统一管理 API 密钥、模型调用和用量账单。</p><div class="identity-facts"><span>按需接入</span><span>用量可查</span><span>权限可控</span></div><router-link to="/pricing">查看模型与价格 →</router-link></aside>
    <div class="identity-card"><div class="eyebrow">{{ eyebrow }}</div><h2>{{ title }}</h2><p class="identity-description">{{ description }}</p><slot /></div>
  </section>
</template>

<style>
.identity-page{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,460px);gap:70px;align-items:start;max-width:1080px;margin:0 auto;padding:72px 30px 64px}.identity-story{padding:35px 0}.identity-story h1{font-size:32px;color:#18384c;line-height:1.6;letter-spacing:-1px;margin:19px 0}.identity-story p{font-size:13px;color:#7b8e9f;line-height:1.9;max-width:335px}.identity-facts{display:flex;gap:9px;flex-wrap:wrap;margin:25px 0 35px}.identity-facts span{font-size:10px;color:#60898e;border:1px solid #dce9e9;border-radius:5px;padding:6px 9px}.identity-story>a{color:#17867d;text-decoration:none;font-size:12px}.identity-card{background:#fff;border:1px solid #e0e8ee;border-radius:12px;padding:32px;min-width:0}.identity-card h2{font-size:23px;line-height:1.6;color:#18384c;margin:13px 0 9px}.identity-description{font-size:12px;line-height:1.9;color:#8294a4;margin-bottom:24px}.identity-card .el-form{margin-top:22px}.identity-card .el-form-item{margin-bottom:20px}.identity-card .el-form-item__label{font-size:12px;color:#506c7d}.identity-card .el-form>.el-button{width:100%;min-height:42px;margin:4px 0}.identity-card .el-alert{margin:16px 0;font-size:12px;overflow-wrap:anywhere}.identity-card .el-alert__description{line-height:1.8}.identity-field-hint{font-size:10px;line-height:1.8;color:#8b9ca9;margin-top:7px}.identity-notice{font-size:11px;line-height:1.9;color:#8195a3;padding:16px 0 0;border-top:1px solid #e8eef2;margin-top:22px}.identity-actions{display:flex;gap:14px;flex-wrap:wrap;align-items:center;margin-top:22px;font-size:12px}.identity-actions a{color:#17867d;text-decoration:none}.identity-capabilities{font-size:11px;color:#8497a5;margin-bottom:14px}.identity-card .el-button.is-link{white-space:normal;height:auto;line-height:1.8;text-align:left}.identity-result{margin:25px 0 10px}.identity-result p{font-size:12px;line-height:1.9;color:#6b8494;margin-top:14px}
@media(max-width:850px){.identity-page{gap:30px;grid-template-columns:minmax(0,1fr) minmax(0,430px);padding:42px 24px}.identity-story h1{font-size:26px}.identity-card{padding:25px}}
@media(max-width:650px){.identity-page{display:block;padding:28px 18px 40px}.identity-story{padding:5px 4px 25px}.identity-story h1{font-size:25px;line-height:1.5;margin:12px 0}.identity-story h1 br{display:none}.identity-story p{font-size:11px;max-width:none}.identity-facts{margin:15px 0}.identity-story>a{display:none}.identity-card{padding:25px 22px}.identity-card h2{font-size:22px}}
</style>
