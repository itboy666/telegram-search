<script setup lang="ts">
import { onMounted, ref } from "vue";
import Brand from "../components/Brand.vue";
import IosIcon from "../components/IosIcon.vue";
import { api } from "../lib/api";
const name = ref("星索");
onMounted(async () => {
  try {
    name.value = (await api("/api/public/config")).site_name;
  } catch {}
});
</script>
<template>
  <div class="site-shell">
    <header class="site-header">
      <div class="site-brand-wrap">
        <Brand :name="name" /><span class="site-edition">DISCOVER MORE</span>
      </div>
      <nav aria-label="网站导航">
        <router-link to="/">发现资源</router-link
        ><router-link to="/user/collections">我的收藏</router-link
        ><router-link to="/user/security" class="site-security-link"
          >账户安全</router-link
        ><router-link to="/user" class="site-account"
          ><IosIcon name="user" />个人中心 <IosIcon name="right"
        /></router-link>
      </nav>
    </header>
    <main id="main" tabindex="-1"><router-view /></main>
    <footer class="site-footer">
      <span
        ><strong>{{ name }} STARINDEX</strong>连接内容，发现更多可能。</span
      ><span
        >公开资源目录
        <span class="footer-separator">/</span> 用户提交需审核</span
      >
    </footer>
  </div>
</template>
