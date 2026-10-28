package com.istarvin.fetcharr.ui.incoming

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.CalendarMonth
import androidx.compose.material.icons.outlined.Check
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.Movie
import androidx.compose.material.icons.outlined.Title
import androidx.compose.material.icons.outlined.Tv
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Surface
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import com.istarvin.fetcharr.ui.common.screenGutter
import com.istarvin.fetcharr.ui.common.useWideLayout
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
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
    /** Placed after the last chip, for example a chip that adds a category. */
    trailing: (@Composable () -> Unit)? = null,
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
        verticalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        categories.forEach { category ->
            val selected = category.id == selectedId
            FilterChip(
                modifier = Modifier.heightIn(min = 48.dp),
                selected = selected,
                onClick = { onSelect(category.id) },
                label = { Text(category.label) },
                leadingIcon = {
                    Icon(
                        if (selected) Icons.Outlined.Check else layoutIcon(category.layout),
                        contentDescription = null,
                        modifier = Modifier.size(FilterChipDefaults.IconSize),
                    )
                },
            )
        }
        trailing?.invoke()
    }
}

fun layoutIcon(layout: String?): ImageVector = when (layout) {
    CategoryOption.LAYOUT_MOVIE -> Icons.Outlined.Movie
    CategoryOption.LAYOUT_TV -> Icons.Outlined.Tv
    else -> Icons.Outlined.Folder
}

@Composable
private fun TextField(
    value: String,
    onChange: (String) -> Unit,
    labelRes: Int,
    modifier: Modifier = Modifier,
    numeric: Boolean = false,
    isError: Boolean = false,
    icon: ImageVector? = null,
) {
    OutlinedTextField(
        value = value,
        onValueChange = onChange,
        modifier = modifier,
        label = { Text(stringResource(labelRes)) },
        leadingIcon = icon?.let { { Icon(it, contentDescription = null) } },
        singleLine = true,
        isError = isError,
        keyboardOptions = KeyboardOptions(keyboardType = if (numeric) KeyboardType.Number else KeyboardType.Text),
    )
}

/** The fields of one item for a category [layout]: movie, tv or flat (spec 5.1). */
@Composable
fun ItemFormFields(layout: String, form: ItemForm, actions: ItemFormActions, modifier: Modifier = Modifier) {
    Column(modifier, verticalArrangement = Arrangement.spacedBy(12.dp)) {
        when (layout) {
            CategoryOption.LAYOUT_MOVIE -> {
                TextField(form.title, actions.onTitle, R.string.field_title, Modifier.fillMaxWidth(), icon = Icons.Outlined.Title)
                TextField(form.year, actions.onYear, R.string.field_year, Modifier.fillMaxWidth(), numeric = true, isError = !form.yearValid, icon = Icons.Outlined.CalendarMonth)
            }
            CategoryOption.LAYOUT_TV -> {
                TextField(form.show, actions.onShow, R.string.field_show, Modifier.fillMaxWidth(), icon = Icons.Outlined.Tv)
                ResponsiveFormRow(
                    first = { TextField(form.season, actions.onSeason, R.string.field_season, it, numeric = true) },
                    second = { TextField(form.episode, actions.onEpisode, R.string.field_episode, it, numeric = true) },
                )
                TextField(form.episodeTitle, actions.onEpisodeTitle, R.string.field_episode_title, Modifier.fillMaxWidth(), icon = Icons.Outlined.Title)
                TextField(form.year, actions.onYear, R.string.field_year, Modifier.fillMaxWidth(), numeric = true, isError = !form.yearValid, icon = Icons.Outlined.CalendarMonth)
            }
            else -> TextField(form.title, actions.onTitle, R.string.field_title, Modifier.fillMaxWidth(), icon = Icons.Outlined.Title)
        }
    }
}

/** Show, Season and Year of the season layout. */
@Composable
fun SeasonFormFields(form: ItemForm, actions: ItemFormActions, modifier: Modifier = Modifier) {
    Column(modifier, verticalArrangement = Arrangement.spacedBy(12.dp)) {
        TextField(form.show, actions.onShow, R.string.field_show, Modifier.fillMaxWidth(), icon = Icons.Outlined.Tv)
        ResponsiveFormRow(
            first = { TextField(form.season, actions.onSeason, R.string.field_season, it, numeric = true) },
            second = { TextField(form.year, actions.onYear, R.string.field_year, it, numeric = true, isError = !form.yearValid, icon = Icons.Outlined.CalendarMonth) },
        )
    }
}

/** Related fields stay together while sections have room to breathe. */
@Composable
fun FormSection(title: String, icon: ImageVector? = null, content: @Composable () -> Unit) {
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            if (icon != null) Icon(icon, contentDescription = null, tint = MaterialTheme.colorScheme.primary, modifier = Modifier.size(20.dp))
            Text(
                title,
                style = MaterialTheme.typography.titleSmall,
                color = MaterialTheme.colorScheme.onSurface,
                modifier = Modifier.semantics { heading() },
            )
        }
        content()
    }
}

/** Use the available form width, and stack even on wide screens at large font scales. */
@Composable
fun ResponsiveFormRow(
    first: @Composable (Modifier) -> Unit,
    second: @Composable (Modifier) -> Unit,
    trailing: (@Composable () -> Unit)? = null,
) {
    // Wide windows use 24dp gutters; keep each field usable within narrower containers.
    val fontScale = LocalDensity.current.fontScale
    val wideWindow = useWideLayout()
    BoxWithConstraints(Modifier.fillMaxWidth()) {
        if (wideWindow && maxWidth >= 552.dp && fontScale <= 1.3f) {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    first(Modifier.weight(1f))
                    second(Modifier.weight(1f))
                }
                if (trailing != null) Box(Modifier.align(Alignment.End)) { trailing() }
            }
        } else {
            Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                first(Modifier.fillMaxWidth())
                second(Modifier.fillMaxWidth())
                if (trailing != null) Box(Modifier.align(Alignment.End)) { trailing() }
            }
        }
    }
}

/** Shared persistent actions; wrapping keeps long labels and large type reachable. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun FormFooter(modifier: Modifier = Modifier, content: @Composable () -> Unit) {
    Surface(modifier.fillMaxWidth(), color = MaterialTheme.colorScheme.surface) {
        Column {
            HorizontalDivider()
            Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
                FlowRow(
                    Modifier.widthIn(max = 720.dp).fillMaxWidth().padding(horizontal = screenGutter(), vertical = 12.dp),
                    horizontalArrangement = Arrangement.spacedBy(12.dp, Alignment.End),
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                    itemVerticalAlignment = Alignment.CenterVertically,
                ) { content() }
            }
        }
    }
}
