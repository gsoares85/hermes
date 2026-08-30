# Documentação do Hermes

Esta pasta é o sistema de gestão do projeto. Tudo que o Claude Code produz fora de código de
aplicação mora aqui, versionado junto com o repositório.

## Como usar

| Quero... | Comando | Produz |
|---|---|---|
| Pesquisar mercado e propor funcionalidades | `/market-research <tema>` | `project-manager/market-research/MR-*.md` + entradas em `backlog.md` |
| Criar uma task bem formada | `/create-task <ideia>` | `project-management/tasks/TASK-XXXX-*.md` + atualização do `index.md` |
| Planejar a implementação de uma task | `/plan-task TASK-XXXX` | `plans/PLAN-TASK-XXXX-*.md` |
| Gerar a descrição do PR | `/pr-description` | `pr-descriptions/PR-*.md` |
| Revisar o PR (código + segurança) | `/pr-review` | `pr-reviews/REVIEW-PR-*.md` |

O ponto de partida de qualquer sessão é `project-management/index.md`: a tabela de tasks e o
roteiro com a ordem de desenvolvimento.

## O que ler antes de implementar

1. `technical/ARCHITECTURE.md` — onde cada coisa mora e por quê.
2. `technical/FEATURE_CATALOG.md` — o que a funcionalidade deve fazer (procure pelo ID).
3. A task em `project-management/tasks/` e o plano correspondente em `plans/`.
4. Os ADRs relevantes em `decisions/`.

## Regras de manutenção

- Toda task referencia os IDs de funcionalidade que implementa (`SYN-08`, `BKP-11`...).
- Toda decisão estrutural vira um ADR. Se a discussão durou mais de dez minutos, é ADR.
- `index.md` é a fonte da verdade de status. Se o status está só no arquivo da task, está errado.
- Planos concluídos não são apagados: viram histórico do porquê das coisas.
