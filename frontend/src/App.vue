<template>
  <div>
    <nav class="topnav">
      <span class="brand">储能电站充放计划</span>
      <router-link to="/plan">运行计划</router-link>
      <router-link to="/settings">参数与电价</router-link>
      <router-link to="/versions">计划版本</router-link>
    </nav>
    <div class="page">
      <router-view />
    </div>
  </div>
</template>
