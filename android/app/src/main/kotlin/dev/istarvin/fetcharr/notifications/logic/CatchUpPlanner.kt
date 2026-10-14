package dev.istarvin.fetcharr.notifications.logic

import dev.istarvin.fetcharr.data.network.dto.JobDto

data class CatchUp(val jobsToProcess: List<JobDto>, val newLastSeen: Long)

object CatchUpPlanner {
    /**
     * [finished] is the response of `GET /api/jobs?finished_after=<lastSeen>`. The server's filter is exclusive
     * (strictly later `finished_at`), so this does the same defensively: jobs with finishedAt <= [lastSeen] or without
     * a finishedAt are dropped. The rest are ordered by finishedAt ascending (ties by id, for determinism).
     * newLastSeen = max(lastSeen, max finishedAt) and never goes backwards.
     */
    fun plan(lastSeen: Long, finished: List<JobDto>): CatchUp {
        val todo = finished
            .filter { (it.finishedAt ?: Long.MIN_VALUE) > lastSeen }
            .sortedWith(compareBy<JobDto> { it.finishedAt }.thenBy { it.id })
        val newest = todo.lastOrNull()?.finishedAt ?: lastSeen
        return CatchUp(todo, maxOf(lastSeen, newest))
    }

    /** First run (nothing stored) starts at [now], so old history never triggers notifications. */
    fun initialLastSeen(stored: Long?, now: Long): Long = stored ?: now
}
