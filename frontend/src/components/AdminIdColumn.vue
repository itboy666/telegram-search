<template>
  <el-table-column
    prop="id"
    label="ID"
    width="96"
    class-name="database-id-column"
  >
    <template #default="{ row }"
      ><span class="database-id">{{ row.id }}</span></template
    >
  </el-table-column>
</template>
