package com.istarvin.fetcharr.ui.incoming

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import com.istarvin.fetcharr.R
import com.istarvin.fetcharr.incoming.CategoryOption

/** Callbacks for [ItemFormFields]. */
class ItemFormActions(
    val onTitle: (String) -> Unit,
    val onYear: (String) -> Unit,
    val onShow: (String) -> Unit,
    val onSeason: (String) -> Unit,
    val onEpisode: (String) -> Unit,
    val onEpisodeTitle: (String) -> Unit,
)

@OptIn(ExperimentalLayoutApi::class)
@Composable
fun CategoryChips(
    categories: List<CategoryOption>,
    selectedId: String?,
    onSelect: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    if (categories.isEmpty()) {
        Text(
            stringResource(R.string.incoming_no_categories),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = modifier,
        )
        return
    }
    FlowRow(
        modifier = modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        categories.forEach { category ->
            FilterChip(
                selected = category.id == selectedId,
                onClick = { onSelect(category.id) },
                label = { Text(category.label) },
            )
        }
    }
}

@Composable
private fun TextField(
    value: String,
    onChange: (String) -> Unit,
    labelRes: Int,
    modifier: Modifier = Modifier,
    numeric: Boolean = false,
    isError: Boolean = false,
) {
    OutlinedTextField(
        value = value,
        onValueChange = onChange,
        modifier = modifier,
        label = { Text(stringResource(labelRes)) },
        singleLine = true,
        isError = isError,
        keyboardOptions = KeyboardOptions(keyboardType = if (numeric) KeyboardType.Number else KeyboardType.Text),
    )
}

/** The fields of one item for a category [layout]: movie, tv or flat (spec 5.1). */
@Composable
fun ItemFormFields(layout: String, form: ItemForm, actions: ItemFormActions, modifier: Modifier = Modifier) {
    Column(modifier, verticalArrangement = Arrangement.spacedBy(8.dp)) {
        when (layout) {
            CategoryOption.LAYOUT_MOVIE -> {
                TextField(form.title, actions.onTitle, R.string.field_title, Modifier.fillMaxWidth())
                TextField(form.year, actions.onYear, R.string.field_year, Modifier.fillMaxWidth(), numeric = true, isError = !form.yearValid)
            }
            CategoryOption.LAYOUT_TV -> {
                TextField(form.show, actions.onShow, R.string.field_show, Modifier.fillMaxWidth())
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    TextField(form.season, actions.onSeason, R.string.field_season, Modifier.weight(1f), numeric = true)
                    TextField(form.episode, actions.onEpisode, R.string.field_episode, Modifier.weight(1f), numeric = true)
                }
                TextField(form.episodeTitle, actions.onEpisodeTitle, R.string.field_episode_title, Modifier.fillMaxWidth())
                TextField(form.year, actions.onYear, R.string.field_year, Modifier.fillMaxWidth(), numeric = true, isError = !form.yearValid)
            }
            else -> TextField(form.title, actions.onTitle, R.string.field_title, Modifier.fillMaxWidth())
        }
    }
}

/** Show, Season and Year of the season layout. */
@Composable
fun SeasonFormFields(form: ItemForm, actions: ItemFormActions, modifier: Modifier = Modifier) {
    Column(modifier, verticalArrangement = Arrangement.spacedBy(8.dp)) {
        TextField(form.show, actions.onShow, R.string.field_show, Modifier.fillMaxWidth())
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            TextField(form.season, actions.onSeason, R.string.field_season, Modifier.weight(1f), numeric = true)
            TextField(form.year, actions.onYear, R.string.field_year, Modifier.weight(1f), numeric = true, isError = !form.yearValid)
        }
    }
}
