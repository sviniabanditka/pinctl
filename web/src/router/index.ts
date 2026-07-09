import { createRouter, createWebHistory } from 'vue-router'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'home', component: () => import('../views/HomeView.vue') },
    {
      path: '/pipelines/:id',
      name: 'pipeline-editor',
      component: () => import('../views/PipelineEditorView.vue'),
      props: true,
      // Consumed by App.vue to give the editor the full viewport with no
      // header-induced page scrollbar — see App.vue's .content-flush.
      meta: { fullscreen: true },
    },
    // Bookmarked pre-consolidation URLs (docs/11-web-ui.md used to split
    // these into their own screens) — send them home rather than 404.
    { path: '/boards', redirect: '/' },
    { path: '/devices', redirect: '/' },
    { path: '/pipelines', redirect: '/' },
    { path: '/:pathMatch(.*)*', name: 'not-found', component: () => import('../views/NotFoundView.vue') },
  ],
})

export default router
