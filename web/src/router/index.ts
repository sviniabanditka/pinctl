import { createRouter, createWebHistory } from 'vue-router'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'dashboard', component: () => import('../views/DashboardView.vue') },
    { path: '/devices', name: 'devices', component: () => import('../views/DevicesView.vue') },
    { path: '/pipelines', name: 'pipelines', component: () => import('../views/PipelinesView.vue') },
    {
      path: '/pipelines/:id',
      name: 'pipeline-editor',
      component: () => import('../views/PipelineEditorView.vue'),
      props: true,
    },
    { path: '/:pathMatch(.*)*', name: 'not-found', component: () => import('../views/NotFoundView.vue') },
  ],
})

export default router
