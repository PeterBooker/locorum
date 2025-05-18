import type { types } from '../../../wailsjs/go/models';
import { StartSite } from '../../../wailsjs/go/sites/SiteManager';
import { PlayIcon } from '@heroicons/react/24/solid';

import { useParams } from 'react-router';

export default function Site({ sites }: { sites: types.Site[] }) {
    let { siteId } = useParams();

    const site = sites.find((site) => site.id === siteId);

    if (!site) {
        return null;
    }

    return (
        <div className="site">
            <PlayIcon
                className="h-5 w-5 text-gray-500"
                onClick={() => StartSite(site.id)}
            />

            <h1>{ site.name }</h1>

            <p>Slug: { site.slug }</p>
            <p>URL: https://{ site.domain }</p>
        </div>
    )
}
